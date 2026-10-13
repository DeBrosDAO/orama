package clusterreg

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

// These hex strings are the bytes chain/x/relay/types MsgRegisterRelay.Marshal and MsgUpdateRelay.Marshal
// write for the same message (relay_wire_test.go there asserts the same constants), so this encoder
// and the chain's cannot drift apart. Exit is field 4, a varint, and is absent when false.
const (
	registerRelayExitHex   = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611a14111111111111111111111111111111111111111120012a402222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222232093333333333333333333a1044444444444444444444444444444444"
	registerRelayNoExitHex = "0a2c6f72616d613139726c34636d32686d7238616679346b6c6470787a33666b61346a677571306135747570307312066e6f64652d611a1411111111111111111111111111111111111111112a402222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222232093333333333333333333a1044444444444444444444444444444444"
	// crossCertGoldenHex is chain/x/relay/types CrossCertMessage("node-a", 20 x 0x11, false) and
	// crossCertExitGoldenHex the same with exit true.
	crossCertGoldenHex     = "6f72616d612f72656c61792f7273612d63726f73732d636572742f763200" + "6e6f64652d61" + "00" + "1111111111111111111111111111111111111111" + "00" + "00"
	crossCertExitGoldenHex = "6f72616d612f72656c61792f7273612d63726f73732d636572742f763200" + "6e6f64652d61" + "00" + "1111111111111111111111111111111111111111" + "00" + "01"
)

func validRelay() RelayRegistration {
	return RelayRegistration{
		Operator:         "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s",
		NodeID:           "node-a",
		RSAFingerprint:   bytes.Repeat([]byte{0x11}, RelayFingerprintLen),
		Exit:             true,
		Ed25519Signature: bytes.Repeat([]byte{0x22}, RelaySignatureLen),
		RSAPublicKey:     bytes.Repeat([]byte{0x33}, 9),
		RSASignature:     bytes.Repeat([]byte{0x44}, 16),
	}
}

func TestEncodeRegisterRelay_matchesChainMarshal(t *testing.T) {
	r := validRelay()
	if got := hex.EncodeToString(EncodeRegisterRelay(r)); got != registerRelayExitHex {
		t.Errorf("exit wire %s", got)
	}
	r.Exit = false
	if got := hex.EncodeToString(EncodeRegisterRelay(r)); got != registerRelayNoExitHex {
		t.Errorf("non-exit wire %s", got)
	}
}

func TestEncodeUpdateRelay_hasRegisterRelaysFields(t *testing.T) {
	if got := hex.EncodeToString(EncodeUpdateRelay(validRelay())); got != registerRelayExitHex {
		t.Errorf("update wire %s", got)
	}
}

func TestRelayTypeURLs(t *testing.T) {
	if RegisterRelayTypeURL != "/orama.relay.v1.MsgRegisterRelay" || UpdateRelayTypeURL != "/orama.relay.v1.MsgUpdateRelay" {
		t.Errorf("type URLs %q %q", RegisterRelayTypeURL, UpdateRelayTypeURL)
	}
}

// rsaProofGoldenHex is chain/x/relay/types RSAProofMessage("node-a", 32 x 0x55, false) and
// rsaProofExitGoldenHex the same with exit true.
const (
	rsaProofGoldenHex = "6f72616d612f72656c61792f7273612d70726f6f662f7632" + "00" + "6e6f64652d61" + "00" +
		"5555555555555555555555555555555555555555555555555555555555555555" + "00" + "00"
	rsaProofExitGoldenHex = "6f72616d612f72656c61792f7273612d70726f6f662f7632" + "00" + "6e6f64652d61" + "00" +
		"5555555555555555555555555555555555555555555555555555555555555555" + "00" + "01"
)

func TestRelayRSAProofMessage_matchesTheChain(t *testing.T) {
	if got := RelayRSAProofMessage("node-a", bytes.Repeat([]byte{0x55}, 32), false); hex.EncodeToString(got) != rsaProofGoldenHex {
		t.Errorf("rsa proof message %x", got)
	}
	if got := RelayRSAProofMessage("node-a", bytes.Repeat([]byte{0x55}, 32), true); hex.EncodeToString(got) != rsaProofExitGoldenHex {
		t.Errorf("rsa proof message for an exit %x", got)
	}
}

func TestRelayCrossCertMessage_matchesTheChain(t *testing.T) {
	got := RelayCrossCertMessage("node-a", bytes.Repeat([]byte{0x11}, RelayFingerprintLen), false)
	if hex.EncodeToString(got) != crossCertGoldenHex {
		t.Errorf("cross-cert message %x", got)
	}
	got = RelayCrossCertMessage("node-a", bytes.Repeat([]byte{0x11}, RelayFingerprintLen), true)
	if hex.EncodeToString(got) != crossCertExitGoldenHex {
		t.Errorf("cross-cert message for an exit %x", got)
	}
}

func TestValidateRelay(t *testing.T) {
	if err := ValidateRelay(validRelay()); err != nil {
		t.Fatalf("a good relay was refused: %v", err)
	}
	for name, edit := range map[string]func(*RelayRegistration){
		"no operator":           func(r *RelayRegistration) { r.Operator = "" },
		"a bad operator":        func(r *RelayRegistration) { r.Operator = "orama1notanaddress" },
		"no node id":            func(r *RelayRegistration) { r.NodeID = "" },
		"a node id with a /":    func(r *RelayRegistration) { r.NodeID = "a/b" },
		"a node id too long":    func(r *RelayRegistration) { r.NodeID = strings.Repeat("a", 65) },
		"no fingerprint":        func(r *RelayRegistration) { r.RSAFingerprint = nil },
		"a 19-byte fingerprint": func(r *RelayRegistration) { r.RSAFingerprint = r.RSAFingerprint[:19] },
		"a 21-byte fingerprint": func(r *RelayRegistration) { r.RSAFingerprint = append(r.RSAFingerprint, 0) },
		"no signature":          func(r *RelayRegistration) { r.Ed25519Signature = nil },
		"a 63-byte signature":   func(r *RelayRegistration) { r.Ed25519Signature = r.Ed25519Signature[:63] },
		"no rsa public key":     func(r *RelayRegistration) { r.RSAPublicKey = nil },
		"an rsa key too long":   func(r *RelayRegistration) { r.RSAPublicKey = make([]byte, RelayRSAPublicKeyMaxLen+1) },
		"no rsa signature":      func(r *RelayRegistration) { r.RSASignature = nil },
		"an rsa proof too long": func(r *RelayRegistration) { r.RSASignature = make([]byte, RelayRSASignatureMaxLen+1) },
	} {
		t.Run(name, func(t *testing.T) {
			r := validRelay()
			edit(&r)
			if err := ValidateRelay(r); err == nil {
				t.Fatal("the relay was accepted")
			}
		})
	}
}
