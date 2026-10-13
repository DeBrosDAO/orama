//go:build e2e_fleet

package chain

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// These are the golden messages of chain/x/relay/types (relay_wire_test.go, rsaproof_test.go) for node
// "node-a", twenty 0x11 fingerprint bytes and 32 0x55 ed25519 key bytes; the helpers must build the
// same bytes the chain verifies.
const (
	crossCertGoldenHex     = "6f72616d612f72656c61792f7273612d63726f73732d636572742f763200" + "6e6f64652d61" + "00" + "1111111111111111111111111111111111111111" + "00" + "00"
	crossCertExitGoldenHex = "6f72616d612f72656c61792f7273612d63726f73732d636572742f763200" + "6e6f64652d61" + "00" + "1111111111111111111111111111111111111111" + "00" + "01"
	rsaProofGoldenHex      = "6f72616d612f72656c61792f7273612d70726f6f662f7632" + "00" + "6e6f64652d61" + "00" +
		"5555555555555555555555555555555555555555555555555555555555555555" + "00" + "00"
	rsaProofExitGoldenHex = "6f72616d612f72656c61792f7273612d70726f6f662f7632" + "00" + "6e6f64652d61" + "00" +
		"5555555555555555555555555555555555555555555555555555555555555555" + "00" + "01"
)

func TestRelayMessages_matchTheChainGoldenBytes(t *testing.T) {
	fp := bytes.Repeat([]byte{0x11}, RelayRSAFingerprintLen)
	pub := ed25519.PublicKey(bytes.Repeat([]byte{0x55}, ed25519.PublicKeySize))
	for name, c := range map[string]struct {
		got  []byte
		want string
	}{
		"cross-cert":      {relayCrossCertMessage("node-a", fp, false), crossCertGoldenHex},
		"cross-cert exit": {relayCrossCertMessage("node-a", fp, true), crossCertExitGoldenHex},
		"rsa proof":       {relayProofMessage("node-a", pub, false), rsaProofGoldenHex},
		"rsa proof exit":  {relayProofMessage("node-a", pub, true), rsaProofExitGoldenHex},
	} {
		if hex.EncodeToString(c.got) != c.want {
			t.Errorf("%s message %x, want %s", name, c.got, c.want)
		}
	}
}

func TestValidRelayExit_bothProofsVerifyAndBindExit(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := NewRelayRSAIdentity(t)
	if len(id.Fingerprint) != RelayRSAFingerprintLen {
		t.Fatalf("fingerprint is %d bytes", len(id.Fingerprint))
	}
	for _, exit := range []bool{false, true} {
		reg := ValidRelayExit(t, "operator", "node-a", id, priv, exit)
		if !ed25519.Verify(pub, relayCrossCertMessage("node-a", id.Fingerprint, exit), reg.Ed25519Sig) {
			t.Errorf("exit=%v: cross-cert does not verify", exit)
		}
		sum := sha256.Sum256(relayProofMessage("node-a", pub, exit))
		if err := rsa.VerifyPKCS1v15(&id.Key.PublicKey, crypto.SHA256, sum[:], reg.RSASig); err != nil {
			t.Errorf("exit=%v: rsa proof does not verify: %v", exit, err)
		}
		if ed25519.Verify(pub, relayCrossCertMessage("node-a", id.Fingerprint, !exit), reg.Ed25519Sig) {
			t.Errorf("exit=%v: cross-cert also verifies for the other exit value", exit)
		}
		other := sha256.Sum256(relayProofMessage("node-a", pub, !exit))
		if rsa.VerifyPKCS1v15(&id.Key.PublicKey, crypto.SHA256, other[:], reg.RSASig) == nil {
			t.Errorf("exit=%v: rsa proof also verifies for the other exit value", exit)
		}
	}
}

func TestRelayRegistration_msgCarriesEveryField(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	reg := ValidRelayExit(t, "operator", "node-a", NewRelayRSAIdentity(t), priv, true)
	for typeURL, m := range map[string]Msg{"/orama.relay.v1.MsgRegisterRelay": reg.Register(), "/orama.relay.v1.MsgUpdateRelay": reg.Update()} {
		if m["@type"] != typeURL {
			t.Errorf("message type %v, want %s", m["@type"], typeURL)
		}
		if m["exit"] != true {
			t.Errorf("%s: exit %v, want true", typeURL, m["exit"])
		}
		for _, field := range []string{"operator", "node_id", "rsa_fingerprint", "ed25519_signature", "rsa_public_key", "rsa_signature"} {
			if s, _ := m[field].(string); s == "" {
				t.Errorf("%s: field %s is empty", typeURL, field)
			}
		}
	}
}
