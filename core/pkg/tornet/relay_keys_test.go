package tornet

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- a Tor relay fingerprint is SHA-1 by definition
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keyedRelayHome writes a relay DataDirectory with an RSA identity key, the fingerprint tor wrote for it
// and an ed25519 secret key file, and returns the directory and the fingerprint's 40 hex digits.
func keyedRelayHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ceremonyKeysDir), 0o700); err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ceremonyKeysDir, KeyRelayIdentity),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	sum := sha1.Sum(x509.MarshalPKCS1PublicKey(&key.PublicKey))
	fingerprint := strings.ToUpper(hex.EncodeToString(sum[:]))
	write(t, filepath.Join(home, dataDirFingerprint), []byte("OramaRelay "+fingerprint+"\n"))
	secret := append([]byte(ed25519SecretTag), make([]byte, ed25519SecretHeaderLen-len(ed25519SecretTag))...)
	secret = append(secret, make([]byte, Ed25519ExpandedLen)...)
	secret[ed25519SecretHeaderLen] = 7
	write(t, filepath.Join(home, ceremonyKeysDir, KeyEd25519Master), secret)
	return home, fingerprint
}

func TestReadRelayKeys_readsTheFingerprintAndTheExpandedSecret(t *testing.T) {
	home, fingerprint := keyedRelayHome(t)
	keys, err := ReadRelayKeys(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ToUpper(hex.EncodeToString(keys.Fingerprint)); got != fingerprint {
		t.Errorf("fingerprint %s, want %s", got, fingerprint)
	}
	if len(keys.Ed25519Expanded) != Ed25519ExpandedLen || keys.Ed25519Expanded[0] != 7 {
		t.Errorf("expanded secret %x", keys.Ed25519Expanded)
	}
	if keys.RSA == nil || keys.RSA.Validate() != nil {
		t.Errorf("the RSA identity key was not read: %+v", keys.RSA)
	}
	if keys.Ed25519ID != "" {
		t.Errorf("ed25519 id %q before tor wrote one", keys.Ed25519ID)
	}
}

func TestReadRelayKeys_readsTheEd25519IDTorWrote(t *testing.T) {
	home, _ := keyedRelayHome(t)
	write(t, filepath.Join(home, dataDirFingerprintEd), []byte("OramaRelay AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE\n"))
	keys, err := ReadRelayKeys(home)
	if err != nil || keys.Ed25519ID != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE" {
		t.Fatalf("keys %+v, %v", keys, err)
	}
}

func TestReadRelayKeys_refusals(t *testing.T) {
	t.Run("a relay that has not started", func(t *testing.T) {
		_, err := ReadRelayKeys(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "has not started") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a fingerprint file of another key", func(t *testing.T) {
		home, _ := keyedRelayHome(t)
		write(t, filepath.Join(home, dataDirFingerprint), []byte("OramaRelay "+strings.Repeat("AB", 20)+"\n"))
		if _, err := ReadRelayKeys(home); err == nil || !strings.Contains(err.Error(), "relay identity key is") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no ed25519 master key", func(t *testing.T) {
		home, _ := keyedRelayHome(t)
		if err := os.Remove(filepath.Join(home, ceremonyKeysDir, KeyEd25519Master)); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRelayKeys(home); err == nil || !strings.Contains(err.Error(), "no ed25519 master identity key") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an ed25519 file that is not tor's", func(t *testing.T) {
		home, _ := keyedRelayHome(t)
		write(t, filepath.Join(home, ceremonyKeysDir, KeyEd25519Master), make([]byte, 96))
		if _, err := ReadRelayKeys(home); err == nil || !strings.Contains(err.Error(), "not a tor ed25519 secret key file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a link in place of the keys directory", func(t *testing.T) {
		home, _ := keyedRelayHome(t)
		elsewhere := t.TempDir()
		keys := filepath.Join(home, ceremonyKeysDir)
		if err := os.Rename(keys, filepath.Join(elsewhere, ceremonyKeysDir)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(elsewhere, ceremonyKeysDir), keys); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRelayKeys(home); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a link in place of a key", func(t *testing.T) {
		home, _ := keyedRelayHome(t)
		path := filepath.Join(home, ceremonyKeysDir, KeyEd25519Master)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/etc/hostname", path); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadRelayKeys(home); err == nil {
			t.Fatal("a symbolic link was followed")
		}
	})
}
