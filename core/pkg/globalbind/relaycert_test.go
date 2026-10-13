package globalbind

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha512"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/clusterreg"
)

func TestSignRelayCrossCert_verifiesAsAnEd25519SignatureOfTheCrossCertMessage(t *testing.T) {
	seed := bytes.Repeat([]byte{0x42}, 32)
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	expanded := sha512.Sum512(seed)
	fingerprint := bytes.Repeat([]byte{0x11}, clusterreg.RelayFingerprintLen)

	got, err := SignRelayCrossCert(expanded[:], "node-a", fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(pub, clusterreg.RelayCrossCertMessage("node-a", fingerprint, false), got) {
		t.Fatal("the cross-certificate does not verify under the key tor's secret derives")
	}
	if ed25519.Verify(pub, clusterreg.RelayCrossCertMessage("node-b", fingerprint, false), got) {
		t.Fatal("the cross-certificate verifies for another node id")
	}
	if ed25519.Verify(pub, clusterreg.RelayCrossCertMessage("node-a", fingerprint, true), got) {
		t.Fatal("the cross-certificate of a non-exit relay verifies for an exit")
	}
	derived, err := ExpandedPublic(expanded[:])
	if err != nil || !bytes.Equal(derived, pub) {
		t.Fatalf("ExpandedPublic = %x, %v; want %x", derived, err, []byte(pub))
	}
}

func TestSignRelayCrossCert_refusesWrongSizes(t *testing.T) {
	good := bytes.Repeat([]byte{0x11}, clusterreg.RelayFingerprintLen)
	if _, err := SignRelayCrossCert(make([]byte, 63), "node-a", good, false); err == nil {
		t.Error("a 63-byte secret was accepted")
	}
	if _, err := SignRelayCrossCert(make([]byte, 64), "node-a", good[:19], false); err == nil {
		t.Error("a 19-byte fingerprint was accepted")
	}
}
