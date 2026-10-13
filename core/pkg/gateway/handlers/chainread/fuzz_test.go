package chainread

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

// FuzzGatewaySanitizeLog: a node's log of a refused transaction is handed to any caller of the public
// gateway. Whatever the log holds, the result is valid UTF-8 without control characters, within the
// length cap, and leaks nothing the patterns exist to hide: it still contains no IPv4 or IPv6
// address, source location, filesystem path or stack trace, and sanitizing it again changes nothing.
func FuzzGatewaySanitizeLog(f *testing.F) {
	for _, s := range []string{
		"insufficient fees; got 10norama required 12norama",
		"panic at 10.0.0.5:26657 in /var/lib/orama/data/x.db",
		"[cosmos/cosmos-sdk@v0.54.4/baseapp/baseapp.go:1066] failed",
		"goroutine 5 [running]:\nmain.go:12",
		"dial tcp [fe80::1]:26657: refused",
		"10.0.0.\xff1", "10.0.\x00" + "0.1", "1\xff0.0.0.1",
		"/va\xffr/lib/x", "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, log string) {
		out := sanitizeLog(log)
		if !utf8.ValidString(out) || utf8.RuneCountInString(out) > logMaxRunes {
			t.Fatalf("invalid or over-long output: %q", out)
		}
		for _, r := range out {
			if unicode.IsControl(r) {
				t.Fatalf("control character %U in %q", r, out)
			}
		}
		// A log that is cut at logMaxRunes can end in a fragment of a token the patterns did not
		// match whole (the first three digits of 1.2.3.45678), which a pattern matches once it is
		// cut. That is not a leak, so the pattern and idempotence checks are for logs the cut
		// cannot reach: the shortest match is "::", which becomes ten runes, so 80 bytes grow to
		// under 512 runes at most.
		if len(log) > 80 {
			return
		}
		for name, pattern := range map[string]interface{ FindString(string) string }{
			"ipv4": ipv4Address, "ipv6": ipv6Address, "source location": sourceLocation, "path": filesystemPath, "stack trace": stackTrace,
		} {
			if leak := pattern.FindString(out); leak != "" {
				t.Fatalf("the output of %q still holds a %s: %q (in %q)", log, name, leak, out)
			}
		}
		if again := sanitizeLog(out); again != out {
			t.Fatalf("sanitizing is not idempotent: %q then %q", out, again)
		}
		if code := sanitizeCodespace(log); code != "" && (len(code) > codespaceMaxLen || strings.ContainsAny(code, " /\n:")) {
			t.Fatalf("codespace %q passed", code)
		}
	})
}

// FuzzGatewayTxProtobuf: the gateway reads two numbers out of transaction and simulation bytes it
// does not trust. For any bytes the readers never panic. For a transaction built from a gas limit it
// reads exactly that limit back, with unknown fields before, between and after (skipped), and with
// the field written twice it takes the last value, the way the chain decodes it; a simulation
// response reads back its gas used.
func FuzzGatewayTxProtobuf(f *testing.F) {
	f.Add(uint64(200000), uint64(7), uint64(9), []byte{0x40, 0x01}, []byte{})
	f.Add(uint64(0), uint64(0), uint64(0), []byte{}, []byte{0x0a})
	f.Fuzz(func(t *testing.T, first, second, used uint64, junk, garbage []byte) {
		_, _ = declaredGasLimit(garbage)
		_, _ = parseGasUsed(garbage)
		_, _, _ = varintField(garbage, 2)
		_, _, _ = bytesField(garbage, 2)
		if !validFields(junk) {
			return
		}

		var fee []byte
		fee = append(fee, junk...)
		fee = protowire.AppendTag(fee, feeGasLimitField, protowire.VarintType)
		fee = protowire.AppendVarint(fee, first)
		fee = append(fee, junk...)
		fee = protowire.AppendTag(fee, feeGasLimitField, protowire.VarintType)
		fee = protowire.AppendVarint(fee, second)
		auth := protowire.AppendBytes(protowire.AppendTag(append([]byte(nil), junk...), authInfoFeeField, protowire.BytesType), fee)
		raw := protowire.AppendBytes(protowire.AppendTag(append([]byte(nil), junk...), txRawAuthInfoField, protowire.BytesType), auth)
		raw = append(raw, junk...)
		got, err := declaredGasLimit(raw)
		if err != nil || got != second {
			t.Fatalf("declaredGasLimit = %d, %v; the transaction declares %d last", got, err, second)
		}

		info := protowire.AppendVarint(protowire.AppendTag(append([]byte(nil), junk...), gasUsedField, protowire.VarintType), used)
		resp := protowire.AppendBytes(protowire.AppendTag(nil, simulateGasInfoField, protowire.BytesType), info)
		if gas, err := parseGasUsed(resp); err != nil || gas != used {
			t.Fatalf("parseGasUsed = %d, %v; the response says %d", gas, err, used)
		}
	})
}

// validFields reports whether b is a sequence of whole protobuf fields none of which uses a field
// number the readers look for, so it can be placed anywhere in a message.
func validFields(b []byte) bool {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 || num <= 2 {
			return false
		}
		b = b[n:]
		skip := protowire.ConsumeFieldValue(num, typ, b)
		if skip < 0 {
			return false
		}
		b = b[skip:]
	}
	return true
}
