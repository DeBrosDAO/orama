package tlsstore

import (
	"encoding/hex"
	"testing"
)

// The same vectors are in orama/caddy/vectors_test.go. Caddy seals what this
// package opens (the exporter) and derives its keys from the file install
// writes from MasterKey; a change on either side fails one of the two.
const (
	vectorMasterHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	vectorMACHex    = "619c0812d37db0ca397535bf2e097d1554f6f72e784d327cdd463281b33e5700"
	vectorSealHex   = "93db0bdb3c098c60f4a50623c2aa8ba4ca2c3750cd61b69b2ac7990d5322f1d5"
	vectorKey       = "certificates/ca/example/example.crt"
	vectorPlain     = "orama tls store vector"
	vectorSealed    = "v1.fOJgV3sz5FQVNCyYK4r7zX/9XbbQfOJ/COpWPCP8VdeB1ysFuiPNONVOfP9Eh7oSKSY="

	vectorClusterSecret = "vector-cluster-secret"
	vectorMasterFromIt  = "d1d7d5ea8b3d27437ebc0e3069d217fd22aca7c380d82d6d98e46032adfc04f1"
)

func vectorKeys(t *testing.T) Keys {
	t.Helper()
	master, _ := hex.DecodeString(vectorMasterHex)
	k, err := DeriveKeys(master)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestDeriveKeys_matchesTheCaddyModule(t *testing.T) {
	k := vectorKeys(t)
	if got := hex.EncodeToString(k.MAC); got != vectorMACHex {
		t.Errorf("MAC key = %s, want %s", got, vectorMACHex)
	}
	if got := hex.EncodeToString(k.Seal); got != vectorSealHex {
		t.Errorf("seal key = %s, want %s", got, vectorSealHex)
	}
}

func TestMasterKey_fromTheClusterSecret(t *testing.T) {
	m, err := MasterKey("  " + vectorClusterSecret + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(m); got != vectorMasterFromIt {
		t.Errorf("master = %s, want %s (the secret is trimmed)", got, vectorMasterFromIt)
	}
}

func TestMasterKey_emptySecret(t *testing.T) {
	if _, err := MasterKey("  "); err == nil {
		t.Fatal("a store key was derived from an empty cluster secret")
	}
}

func TestDeriveKeys_wrongLength(t *testing.T) {
	for _, n := range []int{0, 16, 33} {
		if _, err := DeriveKeys(make([]byte, n)); err == nil {
			t.Errorf("a %d-byte master key was accepted", n)
		}
	}
}

func TestOpen_aValueTheCaddyModuleSealed(t *testing.T) {
	got, err := Open(vectorKeys(t).Seal, vectorKey, vectorSealed)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != vectorPlain {
		t.Errorf("opened %q, want %q", got, vectorPlain)
	}
}
