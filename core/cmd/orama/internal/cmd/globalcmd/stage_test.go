package globalcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckVerifierPinned(t *testing.T) {
	dir := t.TempDir()
	verifier := filepath.Join(dir, "orama-orchard-verifier")
	if err := os.WriteFile(verifier, []byte("verifier"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("verifier"))
	pinned := filepath.Join(dir, "oramad-pinned")
	if err := os.WriteFile(pinned, []byte("code "+hex.EncodeToString(sum[:])+" code"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "oramad-other")
	if err := os.WriteFile(other, []byte("code pinning another verifier"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkVerifierPinned(pinned, verifier); err != nil {
		t.Fatalf("a pair of one release: %v", err)
	}
	if err := checkVerifierPinned(other, verifier); err == nil || !strings.Contains(err.Error(), "does not pin") {
		t.Fatalf("a mismatched pair: %v", err)
	}
	if err := checkVerifierPinned(filepath.Join(dir, "missing"), verifier); err == nil {
		t.Fatal("a missing oramad was accepted")
	}
	if err := checkVerifierPinned(pinned, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing verifier was accepted")
	}
}
