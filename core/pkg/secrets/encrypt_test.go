package secrets

import (
	"encoding/hex"
	"strings"
	"testing"
)

func testKey(t *testing.T, ikm, purpose string) []byte {
	t.Helper()
	k, err := DeriveKey(ikm, purpose)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestEncryptDecrypt_roundTrip(t *testing.T) {
	key := testKey(t, "ikm-one", "purpose-a")
	ct, err := Encrypt("hello", key)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ct, "enc:") || strings.HasPrefix(ct, "enc:v1:") {
		t.Fatalf("legacy encrypt wrote %q", ct)
	}
	got, err := Decrypt(ct, key)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestEncryptVersioned_roundTripAndKeyID(t *testing.T) {
	key := testKey(t, "ikm-one", "purpose-a")
	ct, err := EncryptVersioned("hello", "1", key)
	if err != nil {
		t.Fatal(err)
	}
	env, err := ParseEnvelope(ct)
	if err != nil {
		t.Fatal(err)
	}
	if env.Version != 1 || env.KeyID != "1" {
		t.Fatalf("envelope %+v", env)
	}
	got, err := Decrypt(ct, key)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestDecrypt_failsClosedOnPlaintext(t *testing.T) {
	key := testKey(t, "ikm-one", "purpose-a")
	if _, err := Decrypt("not-encrypted", key); err == nil {
		t.Fatal("plaintext passed through")
	}
	if _, err := Decrypt("", key); err == nil {
		t.Fatal("empty passed through")
	}
}

func TestDecrypt_wrongKeyFails(t *testing.T) {
	a := testKey(t, "ikm-one", "purpose-a")
	b := testKey(t, "ikm-two", "purpose-a")
	ct, err := Encrypt("hello", a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(ct, b); err == nil {
		t.Fatal("wrong key opened the ciphertext")
	}
}

func TestDecryptAny_triesPrevious(t *testing.T) {
	old := testKey(t, "ikm-old", "p")
	cur := testKey(t, "ikm-new", "p")
	ct, err := Encrypt("hello", old)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptAny(ct, cur, old)
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestDeriveKey_emptyIKM(t *testing.T) {
	if _, err := DeriveKey("", "p"); err == nil {
		t.Fatal("empty IKM derived a key")
	}
}

func TestDeriveKey_sameInputsSameKey(t *testing.T) {
	a := testKey(t, "cluster-secret-abc123", "orama-secrets-encryption-v1")
	b := testKey(t, "cluster-secret-abc123", "orama-secrets-encryption-v1")
	if hex.EncodeToString(a) != hex.EncodeToString(b) {
		t.Fatal("not deterministic")
	}
}

func TestIsEncrypted_bothEnvelopes(t *testing.T) {
	if !IsEncrypted("enc:abc") {
		t.Fatal("legacy")
	}
	if !IsEncrypted("enc:v1:1:abc") {
		t.Fatal("versioned")
	}
	if IsEncrypted("{") {
		t.Fatal("json")
	}
}

func TestEncryptVersioned_rejectsEmptyOrSeparatorKeyID(t *testing.T) {
	key := testKey(t, "ikm", "p")
	if _, err := EncryptVersioned("x", "", key); err == nil {
		t.Fatal("empty key id")
	}
	if _, err := EncryptVersioned("x", "1:2", key); err == nil {
		t.Fatal("separator in key id")
	}
}
