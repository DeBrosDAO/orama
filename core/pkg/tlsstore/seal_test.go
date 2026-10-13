package tlsstore

import (
	"strings"
	"testing"
)

func TestSeal_roundTrip(t *testing.T) {
	k := vectorKeys(t)
	sealed, err := Seal(k.Seal, "acme/ca/users/a/a.key", []byte("PRIVATE KEY"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "PRIVATE KEY") {
		t.Fatal("the sealed value holds the plaintext")
	}
	if !IsSealed(sealed) {
		t.Fatal("a sealed value is not recognised as sealed")
	}
	got, err := Open(k.Seal, "acme/ca/users/a/a.key", sealed)
	if err != nil || string(got) != "PRIVATE KEY" {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestSeal_emptyValue(t *testing.T) {
	k := vectorKeys(t)
	sealed, err := Seal(k.Seal, "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Open(k.Seal, "a", sealed)
	if err != nil || len(got) != 0 {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

// A value moved to another key — one certificate's key read as another's —
// does not open.
func TestOpen_refusesAValueStoredUnderAnotherKey(t *testing.T) {
	k := vectorKeys(t)
	if _, err := Open(k.Seal, "certificates/ca/other/other.crt", vectorSealed); err == nil {
		t.Fatal("a value sealed for one key opened under another")
	}
}

func TestOpen_refusesAnotherClustersKey(t *testing.T) {
	other, err := KeysFromClusterSecret("another cluster")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(other.Seal, vectorKey, vectorSealed); err == nil {
		t.Fatal("a value opened under another cluster's key")
	}
}

func TestIsSealed_refusesPlaintextAndMalformed(t *testing.T) {
	for _, v := range []string{
		"", "-----BEGIN CERTIFICATE-----", "v1.", "v1.not base64!", "v1.AAAA", "v2." + strings.TrimPrefix(vectorSealed, "v1."),
	} {
		if IsSealed(v) {
			t.Errorf("IsSealed(%q) = true", v)
		}
	}
}
