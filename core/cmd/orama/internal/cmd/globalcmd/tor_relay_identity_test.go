package globalcmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- a Tor relay fingerprint is SHA-1 by definition
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/globalbind"
)

const (
	relayTestChain    = "orama-stagenet-2"
	relayTestOperator = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
)

// torRelayHome writes a relay DataDirectory the way tor leaves it and returns it with the ed25519
// public key of its master identity and the fingerprint (40 upper-case hex digits).
func torRelayHome(t *testing.T) (home string, pub ed25519.PublicKey, fingerprint string) {
	t.Helper()
	home = t.TempDir()
	keys := filepath.Join(home, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})
	sum := sha1.Sum(x509.MarshalPKCS1PublicKey(&rsaKey.PublicKey))
	fingerprint = strings.ToUpper(hex.EncodeToString(sum[:]))
	seed := bytes.Repeat([]byte{0x33}, ed25519.SeedSize)
	expanded := sha512.Sum512(seed)
	pub = ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	header := append([]byte("== ed25519v1-secret: type0 =="), make([]byte, 32-len("== ed25519v1-secret: type0 =="))...)
	for name, content := range map[string][]byte{
		"keys/secret_id_key":                pemKey,
		"keys/ed25519_master_id_secret_key": append(header, expanded[:]...),
		"fingerprint":                       []byte("OramaRelay " + fingerprint + "\n"),
		"fingerprint-ed25519":               []byte("OramaRelay " + base64.RawStdEncoding.EncodeToString(pub) + "\n"),
	} {
		if err := os.WriteFile(filepath.Join(home, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, pub, fingerprint
}

func TestRelayIdentityDoc_signsWhatTheChainChecksAndPrintsNoSecret(t *testing.T) {
	home, pub, fingerprint := torRelayHome(t)
	doc, err := relayIdentityDoc(home, relayTestChain, relayTestOperator, "node-a", false)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := globalbind.ParseRelayIdentity(doc, relayTestChain, relayTestOperator, "node-a", false)
	if err != nil {
		t.Fatalf("what the command prints does not verify: %v", err)
	}
	if got := strings.ToUpper(hex.EncodeToString(identity.Fingerprint)); got != fingerprint {
		t.Errorf("fingerprint %s, want %s", got, fingerprint)
	}
	if !bytes.Equal(identity.Binding.Pubkey, pub) {
		t.Errorf("the binding is for %x, not the relay's master identity %x", identity.Binding.Pubkey, []byte(pub))
	}
	seed := sha512.Sum512(bytes.Repeat([]byte{0x33}, ed25519.SeedSize))
	if strings.Contains(string(doc), hex.EncodeToString(seed[:32])) || strings.Contains(string(doc), hex.EncodeToString(seed[32:])) {
		t.Error("the relay's secret key is in the output")
	}
}

func TestRelayIdentityDoc_signaturesCoverTheExitDeclaration(t *testing.T) {
	home, _, _ := torRelayHome(t)
	for _, exit := range []bool{false, true} {
		doc, err := relayIdentityDoc(home, relayTestChain, relayTestOperator, "node-a", exit)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := globalbind.ParseRelayIdentity(doc, relayTestChain, relayTestOperator, "node-a", exit); err != nil {
			t.Errorf("exit=%v: what the command prints does not verify for its own declaration: %v", exit, err)
		}
		if _, err := globalbind.ParseRelayIdentity(doc, relayTestChain, relayTestOperator, "node-a", !exit); err == nil {
			t.Errorf("exit=%v: the printed identity verifies for the opposite declaration", exit)
		}
	}
}

func TestRelayIdentityDoc_refusals(t *testing.T) {
	t.Run("a relay that has not started", func(t *testing.T) {
		if _, err := relayIdentityDoc(t.TempDir(), relayTestChain, relayTestOperator, "node-a", false); err == nil || !strings.Contains(err.Error(), "has not started") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an identity tor wrote for another key", func(t *testing.T) {
		home, _, _ := torRelayHome(t)
		other := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 32))
		if err := os.WriteFile(filepath.Join(home, "fingerprint-ed25519"), []byte("OramaRelay "+other+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := relayIdentityDoc(home, relayTestChain, relayTestOperator, "node-a", false); err == nil || !strings.Contains(err.Error(), "tor wrote the identity") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an operator that is not an address", func(t *testing.T) {
		home, _, _ := torRelayHome(t)
		if _, err := relayIdentityDoc(home, relayTestChain, "ORAMA1X", "node-a", false); err == nil {
			t.Fatal("a bad operator was accepted")
		}
	})
}
