package wallet

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
)

func challengeFor(t *testing.T, address string, chain siw.Chain) string {
	t.Helper()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	m := &siw.Message{
		Chain: chain, Domain: "e2e-ab12.dbrsteting.bid", Address: address,
		Statement: "Sign in to Orama", URI: "https://e2e-ab12.dbrsteting.bid", ChainID: "1",
		Nonce: "abcdefgh12345678", IssuedAt: now, ExpirationTime: now.Add(5 * time.Minute),
		Resources: []string{"urn:orama:namespace:e2e"},
	}
	text, err := m.Render()
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestEVMSign_recoversToAddress(t *testing.T) {
	w, err := NewEVM()
	if err != nil {
		t.Fatal(err)
	}
	msg := challengeFor(t, w.Address(), siw.Ethereum)
	sig, err := w.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sig, "0x") || len(sig) != 2+130 {
		t.Fatalf("signature shape %q", sig)
	}
	if v := sig[len(sig)-2:]; v != "1b" && v != "1c" {
		t.Fatalf("recovery byte %s, want 1b or 1c", v)
	}
	got, err := RecoverAddress(msg, sig)
	if err != nil || got != w.Address() {
		t.Fatalf("recovered %s err %v, want %s", got, err, w.Address())
	}
	other, err := RecoverAddress(msg+"x", sig)
	if err != nil || other == w.Address() {
		t.Fatal("a signature verified over a different message")
	}
}

func TestEVMFromHex_knownVector(t *testing.T) {
	// The well-known Hardhat account #0 key: public test material, not a secret.
	w, err := EVMFromHex("0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	if err != nil {
		t.Fatal(err)
	}
	if w.Address() != "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266" {
		t.Fatalf("address %s", w.Address())
	}
	if _, err := EVMFromHex("zz"); err == nil {
		t.Fatal("bad hex accepted")
	}
}

func TestRecoverAddress_malformed(t *testing.T) {
	for _, sig := range []string{"", "0x1234", "0xzz", "0x" + strings.Repeat("00", 65)} {
		if _, err := RecoverAddress("m", sig); err == nil {
			t.Errorf("signature %q accepted", sig)
		}
	}
}

func TestMutate_rendersParseableForgery(t *testing.T) {
	w, _ := NewEVM()
	text := challengeFor(t, w.Address(), siw.Ethereum)
	forged, err := Mutate(text, func(m *SIWEMessage) { m.Domain = "evil.example" })
	if err != nil {
		t.Fatal(err)
	}
	m, err := ParseSIWE(forged)
	if err != nil || m.Domain != "evil.example" || m.Nonce != "abcdefgh12345678" {
		t.Fatalf("forged message: %+v err %v", m, err)
	}
	if _, err := Mutate(text, func(m *SIWEMessage) { m.Statement = "a\nURI: x" }); err == nil {
		t.Fatal("a statement with a newline rendered")
	}
	if _, err := ParseSIWE("not a message"); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestDevice_matchesGatewayVerifier(t *testing.T) {
	for _, mk := range []func() (*Device, error){NewEd25519Device, NewES256Device} {
		d, err := mk()
		if err != nil {
			t.Fatal(err)
		}
		key, err := gwauth.ParseDeviceKey(d.PublicJWK())
		if err != nil {
			t.Fatalf("%s: gateway refused the JWK: %v", d.Alg(), err)
		}
		if key.ID() != d.ID() || !gwauth.ValidDeviceID(d.ID()) {
			t.Fatalf("%s: id %s, gateway computes %s", d.Alg(), d.ID(), key.ID())
		}
		p, err := d.NewProof(ProofRefresh, "e2e-ns", "dv1_refresh")
		if err != nil {
			t.Fatal(err)
		}
		want := gwauth.DeviceProofMessage(gwauth.DeviceProofRefresh, "e2e-ns", "dv1_refresh", p.IssuedAt, p.ID)
		if !bytes.Equal(want, ProofMessage(ProofRefresh, "e2e-ns", "dv1_refresh", p.IssuedAt, p.ID)) {
			t.Fatalf("%s: proof statement differs from the gateway's", d.Alg())
		}
		if err := key.Verify(want, p.Signature); err != nil {
			t.Fatalf("%s: gateway did not verify the proof: %v", d.Alg(), err)
		}
		if err := key.Verify(append(want, 'x'), p.Signature); err == nil {
			t.Fatalf("%s: proof verified over another statement", d.Alg())
		}
	}
}

func TestDevice_ES256DERAccepted(t *testing.T) {
	d, err := NewES256Device()
	if err != nil {
		t.Fatal(err)
	}
	key, _ := gwauth.ParseDeviceKey(d.PublicJWK())
	sig, err := d.SignDER([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if err := key.Verify([]byte("hello"), sig); err != nil {
		t.Fatalf("DER signature refused: %v", err)
	}
	ed, _ := NewEd25519Device()
	if _, err := ed.SignDER([]byte("x")); err == nil {
		t.Fatal("DER on Ed25519 accepted")
	}
}

func TestDevice_privateJWK(t *testing.T) {
	d, _ := NewEd25519Device()
	raw, err := d.PrivateJWK()
	if err != nil || !bytes.Contains(raw, []byte(`"d":"`)) {
		t.Fatalf("private JWK %s err %v", raw, err)
	}
	if _, err := gwauth.ParseDeviceKey(raw); err == nil {
		t.Fatal("gateway accepted a JWK carrying d; the harness must never send it")
	}
	ec, _ := NewES256Device()
	if _, err := ec.PrivateJWK(); err == nil {
		t.Fatal("ES256 private export accepted")
	}
}

func TestProofAt_deterministicStatement(t *testing.T) {
	d, _ := NewEd25519Device()
	at := time.Unix(1790000000, 0)
	p, err := d.ProofAt(ProofRevoke, "ns", "dev-id", at, "0123456789abcdef")
	if err != nil || p.IssuedAt != 1790000000 || p.ID != "0123456789abcdef" {
		t.Fatalf("proof %+v err %v", p, err)
	}
	if got := string(ProofMessage("", "", "", 0, "")); got != DeviceProofVersion+"\n\n\n\n0\n" {
		t.Fatalf("empty statement %q", got)
	}
}

func TestSolana_signatureVerifies(t *testing.T) {
	w, err := NewSolana()
	if err != nil {
		t.Fatal(err)
	}
	msg := challengeFor(t, w.Address(), siw.Solana)
	pub, err := (&gwauth.Service{}).Base58Decode(w.Address())
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("address %s does not decode to a key: %v", w.Address(), err)
	}
	sig, err := base64.StdEncoding.DecodeString(w.Sign(msg))
	if err != nil || !ed25519.Verify(pub, []byte(msg), sig) {
		t.Fatalf("SIWS signature does not verify: %v", err)
	}
}
