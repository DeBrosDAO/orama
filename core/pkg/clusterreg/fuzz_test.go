package clusterreg

import (
	"bytes"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/DeBrosOfficial/network/pkg/netguard"
)

func strField(t *testing.T, fs []field, num protowire.Number) string {
	t.Helper()
	f, ok := get(fs, num)
	if !ok {
		return ""
	}
	return string(f.bytes)
}

// FuzzClusterregRegistration: the registration a cluster signs. When Validate accepts it, every
// endpoint host is one the chain would take as public: no address in a range core/pkg/netguard
// refuses (and the chain's netclass with it), no zone id, no number a resolver reads as an IPv4
// address. The protobuf the encoder writes is canonical (fields in order, empty ones left out) and
// reads back to exactly the registration.
func FuzzClusterregRegistration(f *testing.F) {
	op := "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	f.Add(op, "cluster-1", "example.com", "https://gw.example.com:443\n/ip4/93.184.216.34/tcp/4001", "ipfs://cid")
	f.Add(op, "c", "example.com", "100.64.0.1\n198.18.0.1\n192.0.2.1", "")
	f.Add(op, "c", "example.com", "https://[fe80::1%25eth0]/", "")
	f.Add(op, "c", "example.com", "127.1\n0x7f.1\n2130706433", "")
	f.Fuzz(func(t *testing.T, operator, clusterID, domain, endpoints, metadata string) {
		reg := Registration{Operator: operator, ClusterID: clusterID, BaseDomain: domain, MetadataURI: metadata}
		if endpoints != "" {
			reg.Endpoints = strings.Split(endpoints, "\n")
		}
		if err := Validate(reg); err != nil {
			return
		}
		for _, ep := range reg.Endpoints {
			hosts, err := endpointHosts(ep)
			if err != nil {
				t.Fatalf("Validate accepted %q but endpointHosts refuses: %v", ep, err)
			}
			for _, host := range hosts {
				assertPublicHost(t, ep, host)
			}
		}
		fs := parseFields(t, EncodeRegisterCluster(reg))
		var got []string
		for _, fl := range fs {
			if fl.num == 4 {
				got = append(got, string(fl.bytes))
			}
		}
		if strField(t, fs, 1) != reg.Operator || strField(t, fs, 2) != reg.ClusterID || strField(t, fs, 3) != reg.BaseDomain ||
			strField(t, fs, 5) != reg.MetadataURI || strings.Join(got, "\n") != strings.Join(reg.Endpoints, "\n") {
			t.Fatalf("EncodeRegisterCluster does not read back to %+v", reg)
		}
		last := protowire.Number(0)
		for _, fl := range fs {
			if fl.num < last || (fl.typ == protowire.BytesType && len(fl.bytes) == 0) {
				t.Fatalf("the encoding is not canonical at field %d", fl.num)
			}
			last = fl.num
		}
	})
}

func assertPublicHost(t *testing.T, endpoint, host string) {
	t.Helper()
	trimmed := strings.TrimSuffix(host, ".")
	if trimmed == "" {
		t.Fatalf("accepted %q whose host is empty", endpoint)
	}
	if strings.ContainsAny(trimmed, "%\\ ") {
		t.Fatalf("accepted %q whose host %q carries a zone id or a separator", endpoint, host)
	}
	if addr, err := netip.ParseAddr(trimmed); err == nil {
		if ip := net.IP(addr.Unmap().AsSlice()); netguard.Reserved(ip) || !addr.Unmap().IsGlobalUnicast() {
			t.Fatalf("accepted %q whose host %s is not a public address", endpoint, addr)
		}
		return
	}
	if v4, ok := inetAton(trimmed); ok && !strictDottedQuad(trimmed) {
		t.Fatalf("accepted %q whose host %q a resolver reads as the IPv4 number %#x", endpoint, host, v4)
	}
	if lower := strings.ToLower(trimmed); lower == "localhost" || strings.HasSuffix(lower, ".localhost") || strings.HasSuffix(lower, ".local") {
		t.Fatalf("accepted %q whose host %q is local", endpoint, host)
	}
}

// inetAton reads host the way a C resolver's inet_aton does: one to four numbers (decimal, 0x hex or
// 0 octal), the last filling the rest of the 32 bits.
func inetAton(host string) (uint32, bool) {
	parts := strings.Split(host, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return 0, false
	}
	vals := make([]uint64, len(parts))
	for i, p := range parts {
		if p == "" {
			return 0, false
		}
		base := 10
		switch {
		case len(p) > 1 && (p[:2] == "0x" || p[:2] == "0X"):
			base, p = 16, p[2:]
			if p == "" {
				p = "0"
			}
		case len(p) > 1 && p[0] == '0':
			base, p = 8, p[1:]
		}
		v, err := strconv.ParseUint(p, base, 32)
		if err != nil {
			return 0, false
		}
		vals[i] = v
	}
	var addr uint64
	for i := 0; i < len(vals)-1; i++ {
		if vals[i] > 255 {
			return 0, false
		}
		addr |= vals[i] << (8 * (3 - i))
	}
	if last := vals[len(vals)-1]; last < 1<<(8*(5-len(vals))) {
		return uint32(addr | last), true
	}
	return 0, false
}

func strictDottedQuad(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 || strconv.Itoa(n) != p {
			return false
		}
	}
	return true
}

// FuzzClusterregDirect: the SIGN_MODE_DIRECT document and the transaction built from it. For any
// input Direct never panics; when it builds, the SignDoc and the TxRaw carry the very same body and
// auth info bytes (so the signature over one is a signature over the other), every field reads
// back, fields come in order, and a zero sequence or account number is not written (proto3 never
// writes it, and the wallet refuses to sign bytes that are not canonical).
func FuzzClusterregDirect(f *testing.F) {
	pub := append([]byte{0x02}, bytes.Repeat([]byte{0x11}, 32)...)
	f.Add(RegisterClusterTypeURL, []byte{0x0a, 0x01, 'x'}, pub, uint64(0), "1000", uint64(200000), "orama-stagenet-6", uint64(0), uint64(0), bytes.Repeat([]byte{7}, 64))
	f.Add("/x", []byte{1}, pub, uint64(1<<63), "9", uint64(1), "c", uint64(1<<40), uint64(99), bytes.Repeat([]byte{7}, 64))
	f.Add("", []byte{}, []byte{}, uint64(0), "0", uint64(0), "", uint64(0), uint64(0), []byte{})
	f.Fuzz(func(t *testing.T, typeURL string, msg, pubKey []byte, sequence uint64, fee string, gas uint64, chainID string, account, timeout uint64, signature []byte) {
		d := Direct{TypeURL: typeURL, Msg: msg, PubKey: pubKey, Sequence: sequence, FeeAmount: fee, Gas: gas, ChainID: chainID, AccountNumber: account, TimeoutHeight: timeout}
		doc, err := d.SignDoc()
		raw, rawErr := d.TxRaw(signature)
		if err != nil {
			if rawErr == nil {
				t.Fatal("TxRaw builds what SignDoc refuses")
			}
			return
		}
		top := parseFields(t, doc)
		bodyBz, _ := get(top, 1)
		authBz, _ := get(top, 2)
		if strField(t, top, 3) != chainID {
			t.Fatal("the chain id is not in the SignDoc")
		}
		if v, ok := get(top, 4); ok != (account != 0) || (ok && v.value != account) {
			t.Fatalf("account number %d is written wrongly (present=%v)", account, ok)
		}
		if rawErr == nil {
			rawTop := parseFields(t, raw)
			rb, _ := get(rawTop, 1)
			ra, _ := get(rawTop, 2)
			sig, _ := get(rawTop, 3)
			if !bytes.Equal(rb.bytes, bodyBz.bytes) || !bytes.Equal(ra.bytes, authBz.bytes) || !bytes.Equal(sig.bytes, signature) {
				t.Fatal("the TxRaw does not carry the body and auth info the SignDoc commits to")
			}
		} else if len(signature) == 64 {
			t.Fatalf("TxRaw refused a 64-byte signature: %v", rawErr)
		}
		auth := parseFields(t, authBz.bytes)
		signer, _ := get(auth, 1)
		si := parseFields(t, signer.bytes)
		if v, ok := get(si, 3); ok != (sequence != 0) || (ok && v.value != sequence) {
			t.Fatalf("sequence %d is written wrongly (present=%v)", sequence, ok)
		}
		feeMsg, _ := get(auth, 2)
		fm := parseFields(t, feeMsg.bytes)
		if g, _ := get(fm, 2); g.value != gas {
			t.Fatalf("gas %d is written as %d", gas, g.value)
		}
		coin, _ := get(fm, 1)
		if strField(t, parseFields(t, coin.bytes), 2) != fee {
			t.Fatal("the fee is not in the auth info")
		}
		body := parseFields(t, bodyBz.bytes)
		anyBz, _ := get(body, 1)
		an := parseFields(t, anyBz.bytes)
		if strField(t, an, 1) != typeURL {
			t.Fatal("the type URL is not in the body")
		}
		if m, _ := get(an, 2); !bytes.Equal(m.bytes, msg) {
			t.Fatal("the message is not in the body")
		}
		if v, ok := get(body, 3); ok != (timeout != 0) || (ok && v.value != timeout) {
			t.Fatalf("timeout height %d is written wrongly (present=%v)", timeout, ok)
		}
	})
}

// FuzzClusterregProofs: the proof file `orama storage prove --file` reads is the one the provider
// writes. ParseProofs never panics; an accepted array re-encodes to a MsgSubmitProofs whose
// fields read back to the proofs, and ValidateProofs accepts exactly the proofs of the right
// widths (a 1024-byte leaf, 32-byte siblings, a deal id).
func FuzzClusterregProofs(f *testing.F) {
	leaf := strings.Repeat("ab", proofLeafSize)
	sib := strings.Repeat("cd", proofSiblingSize)
	f.Add([]byte(`[{"deal_id":7,"slot":1,"leaf_index":3,"leaf":"` + leaf + `","siblings":["` + sib + `","` + sib + `"]}]`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"deal_id":0,"leaf":"zz"}]`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		proofs, err := ParseProofs(raw)
		if err != nil {
			return
		}
		if len(proofs) == 0 {
			t.Fatal("ParseProofs accepted an empty list")
		}
		msg := Proofs{Signer: "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s", NodeID: "node-1", Proofs: proofs}
		wellFormed := true
		for _, p := range proofs {
			wellFormed = wellFormed && p.DealID != 0 && len(p.Leaf) == proofLeafSize
			for _, s := range p.Siblings {
				wellFormed = wellFormed && len(s) == proofSiblingSize
			}
		}
		if err := ValidateProofs(msg); (err == nil) != wellFormed {
			t.Fatalf("ValidateProofs error %v for proofs that are well formed=%v", err, wellFormed)
		}
		fs := parseFields(t, EncodeProofs(msg))
		var n int
		for _, fl := range fs {
			if fl.num != 3 {
				continue
			}
			inner := parseFields(t, fl.bytes)
			if v, _ := get(inner, 1); v.value != proofs[n].DealID {
				t.Fatalf("proof %d: deal id %d reads back as %d", n, proofs[n].DealID, v.value)
			}
			if l, _ := get(inner, 4); !bytes.Equal(l.bytes, proofs[n].Leaf) {
				t.Fatalf("proof %d: the leaf does not read back", n)
			}
			n++
		}
		if n != len(proofs) {
			t.Fatalf("%d proofs encoded, %d read back", len(proofs), n)
		}
	})
}

// FuzzClusterregCanonicalAccount: an address CanonicalAccount accepts is lowercase, of the
// account prefix, 20 bytes, and the one canonical encoding of those bytes; it never panics.
func FuzzClusterregCanonicalAccount(f *testing.F) {
	f.Add("orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s")
	f.Add("ORAMA19RL4CM2HMR8AFY4KLDPXZ3FKA4JGUQ0A5TUP0S")
	f.Add("orama1")
	f.Add("")
	f.Fuzz(func(t *testing.T, addr string) {
		got, err := CanonicalAccount(addr)
		if err != nil {
			return
		}
		hrp, data, decodeErr := bech32Decode(got)
		if decodeErr != nil || got != addr || hrp != accountHRP || len(data) != accountBytes || got != strings.ToLower(got) {
			t.Fatalf("CanonicalAccount(%q) = %q (%v, %d bytes)", addr, got, hrp, len(data))
		}
	})
}
