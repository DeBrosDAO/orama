package invite

import (
	"strings"
	"testing"
)

// A join used to need three values transcribed by hand from one machine to
// another, two of which are indistinguishable strings of hex. One value cannot
// be transcribed the wrong way round, and it cannot be partially copied.

func TestEncodeDecode_roundTrip(t *testing.T) {
	want := Invite{
		JoinURL:       "https://node1.orama-devnet.network",
		Token:         strings.Repeat("a", 64),
		CAFingerprint: strings.Repeat("b", 64),
	}

	encoded, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Errorf("round trip changed the invite:\ngot  %+v\nwant %+v", got, want)
	}
}

// The prefix is what makes a later format recognisable rather than guessed at.
func TestEncode_isPrefixed(t *testing.T) {
	encoded, err := Encode(Invite{JoinURL: "https://x", Token: "t"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.HasPrefix(encoded, prefix) {
		t.Errorf("encoded invite %q does not start with %q", encoded, prefix)
	}
}

// The encoding has to survive being pasted into a shell command line and a
// YAML file, so it may not contain quoting-significant characters.
func TestEncode_isSafeToPaste(t *testing.T) {
	encoded, err := Encode(Invite{
		JoinURL:       "https://node1.example.com",
		Token:         strings.Repeat("f", 64),
		CAFingerprint: strings.Repeat("0", 64),
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, bad := range []string{" ", "'", "\"", "/", "+", "=", "\n", "$", "&"} {
		if strings.Contains(encoded, bad) {
			t.Errorf("encoded invite contains %q, which needs shell quoting: %s", bad, encoded)
		}
	}
}

func TestEncode_refusesAnIncompleteInvite(t *testing.T) {
	if _, err := Encode(Invite{JoinURL: "https://x"}); err == nil {
		t.Error("an invite with no token must be refused")
	}
	if _, err := Encode(Invite{Token: "t"}); err == nil {
		t.Error("an invite with no join URL must be refused")
	}
}

// Every token issued before this format is 64 hex characters, and a cluster
// mid-upgrade still hands them out.
func TestDecode_acceptsABareToken(t *testing.T) {
	bare := strings.Repeat("ab", 32)

	got, err := Decode(bare)
	if err != nil {
		t.Fatalf("a bare token must still decode: %v", err)
	}
	if got.Token != bare {
		t.Errorf("Token = %q, want the token itself", got.Token)
	}
	if got.JoinURL != "" || got.CAFingerprint != "" {
		t.Errorf("a bare token carries nothing else, got %+v", got)
	}
}

func TestDecode_rejectsNonsense(t *testing.T) {
	for _, in := range []string{
		"",
		"   ",
		"not-a-token",
		strings.Repeat("z", 64),     // right length, not hex
		strings.Repeat("a", 63),     // hex, wrong length
		prefix + "!!!not-base64!!!", // right prefix, bad body
		prefix + "eyJ4IjoxfQ",       // valid base64 JSON, no token
	} {
		if _, err := Decode(in); err == nil {
			t.Errorf("Decode(%q) must fail", in)
		}
	}
}

// A truncated paste must not decode into a usable invite.
func TestDecode_rejectsATruncatedInvite(t *testing.T) {
	encoded, err := Encode(Invite{
		JoinURL: "https://node1.example.com",
		Token:   strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := Decode(encoded[:len(encoded)-8]); err == nil {
		t.Error("a truncated invite must not decode")
	}
}

func TestDecode_trimsSurroundingWhitespace(t *testing.T) {
	encoded, err := Encode(Invite{JoinURL: "https://x", Token: "t"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := Decode("  " + encoded + "\n"); err != nil {
		t.Errorf("a pasted invite carries whitespace: %v", err)
	}
}

// The fingerprint is read from a gateway URL, which may or may not carry a
// scheme, a port or a path.
func TestNormalizeHostPort(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"example.com", "example.com:443"},
		{"https://example.com", "example.com:443"},
		{"http://example.com", "example.com:443"},
		{"https://example.com/", "example.com:443"},
		{"https://example.com/v1/health", "example.com:443"},
		{"example.com:8443", "example.com:8443"},
		{"https://example.com:8443", "example.com:8443"},
	} {
		if got := normalizeHostPort(tc.in); got != tc.want {
			t.Errorf("normalizeHostPort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFingerprint_reportsAnUnreachableHost(t *testing.T) {
	// 192.0.2.0/24 is TEST-NET-1 (RFC 5737): reserved for documentation and
	// guaranteed not to be routed.
	if _, err := Fingerprint("192.0.2.1:9"); err == nil {
		t.Error("an unreachable host must be an error, not an empty fingerprint")
	}
}
