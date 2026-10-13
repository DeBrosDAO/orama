package install

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/legacylayout"
)

type keyFixture struct{ oramaDir string }

func newKeyFixture(t *testing.T) keyFixture {
	t.Helper()
	return keyFixture{oramaDir: filepath.Join(t.TempDir(), ".orama")}
}

func (f keyFixture) write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f keyFixture) original(name string) string {
	return filepath.Join(legacylayout.SecretsDir(f.oramaDir), name)
}

func (f keyFixture) copyOf(name string) string {
	return filepath.Join(legacylayout.IndexGatewayStateDir(f.oramaDir), name)
}

// record writes the marker orama-node leaves after copying key contents.
func (f keyFixture) record(t *testing.T, contents map[string]string) {
	t.Helper()
	body := "{"
	first := true
	for name, c := range contents {
		if !first {
			body += ","
		}
		first = false
		body += `"` + name + `":"` + legacylayout.KeyDigest([]byte(c)) + `"`
	}
	f.write(t, legacylayout.CopiedKeysMarker(f.oramaDir), body+"}")
}

func removeKeys(t *testing.T, oramaDir string) error {
	t.Helper()
	return removeCopiedSigningKeys(oramaDir, func(string, ...interface{}) {})
}

func TestRemoveCopiedSigningKeys_removesWhatTheNodeCopied(t *testing.T) {
	f := newKeyFixture(t)
	f.write(t, f.original("jwt-signing-key.pem"), "rsa")
	f.write(t, f.original("jwt-eddsa-key.pem"), "ed")
	f.write(t, f.original("cluster-secret"), "s")
	f.write(t, f.copyOf("jwt-signing-key.pem"), "rsa")
	// The gateway replaced its copy of the cluster-derived EdDSA key.
	f.write(t, f.copyOf("jwt-eddsa-key.pem"), "gateway's own")
	f.record(t, map[string]string{"jwt-signing-key.pem": "rsa", "jwt-eddsa-key.pem": "ed"})

	if err := removeKeys(t, f.oramaDir); err != nil {
		t.Fatal(err)
	}
	for _, n := range legacylayout.SigningKeyNames {
		if _, err := os.Lstat(f.original(n)); !os.IsNotExist(err) {
			t.Errorf("%s was not removed (%v)", n, err)
		}
	}
	if _, err := os.Stat(f.original("cluster-secret")); err != nil {
		t.Error("something other than the two signing keys was removed")
	}
}

// Nothing is deleted that the node has not both recorded and copied.
func TestRemoveCopiedSigningKeys_leavesWhatWasNotCopied(t *testing.T) {
	f := newKeyFixture(t)
	f.write(t, f.original("jwt-signing-key.pem"), "rsa")
	f.write(t, f.original("jwt-eddsa-key.pem"), "ed changed since")
	f.write(t, f.copyOf("jwt-eddsa-key.pem"), "ed")
	// RSA recorded but its copy is missing (a run stopped between the two);
	// EdDSA copied, but the original no longer matches the record.
	f.record(t, map[string]string{"jwt-signing-key.pem": "rsa", "jwt-eddsa-key.pem": "ed"})

	if err := removeKeys(t, f.oramaDir); err != nil {
		t.Fatal(err)
	}
	for _, n := range legacylayout.SigningKeyNames {
		if _, err := os.Stat(f.original(n)); err != nil {
			t.Errorf("%s was removed without a matching copy: %v", n, err)
		}
	}
}

func TestRemoveCopiedSigningKeys_noMarkerIsANoOp(t *testing.T) {
	f := newKeyFixture(t)
	f.write(t, f.original("jwt-signing-key.pem"), "rsa")
	f.write(t, f.copyOf("jwt-signing-key.pem"), "rsa")
	if err := removeKeys(t, f.oramaDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.original("jwt-signing-key.pem")); err != nil {
		t.Error("a key the node never recorded copying was removed")
	}
}

// secrets/ replaced by a symlink (the orama user owns the directory above it)
// must not lead root to delete files somewhere else.
func TestRemoveCopiedSigningKeys_refusesASymlinkedSecretsDir(t *testing.T) {
	f := newKeyFixture(t)
	elsewhere := filepath.Join(t.TempDir(), "etc")
	f.write(t, filepath.Join(elsewhere, "jwt-signing-key.pem"), "rsa")
	if err := os.MkdirAll(f.oramaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, legacylayout.SecretsDir(f.oramaDir)); err != nil {
		t.Fatal(err)
	}
	f.write(t, f.copyOf("jwt-signing-key.pem"), "rsa")
	f.record(t, map[string]string{"jwt-signing-key.pem": "rsa"})

	if err := removeKeys(t, f.oramaDir); err == nil {
		t.Fatal("a symlinked secrets/ must be refused")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "jwt-signing-key.pem")); err != nil {
		t.Error("root deleted a file through the symlink")
	}
}

func TestRemoveCopiedSigningKeys_refusesASymlinkedKey(t *testing.T) {
	f := newKeyFixture(t)
	target := filepath.Join(t.TempDir(), "shadow")
	f.write(t, target, "rsa")
	if err := os.MkdirAll(legacylayout.SecretsDir(f.oramaDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.original("jwt-signing-key.pem")); err != nil {
		t.Fatal(err)
	}
	f.write(t, f.copyOf("jwt-signing-key.pem"), "rsa")
	f.record(t, map[string]string{"jwt-signing-key.pem": "rsa"})

	if err := removeKeys(t, f.oramaDir); err == nil {
		t.Fatal("a symlink in place of a key must be refused")
	}
	if _, err := os.Stat(target); err != nil {
		t.Error("the symlink's target was touched")
	}
}

// A FIFO planted where root reads (the marker, or a key) must fail the step,
// not block the upgrade forever with the node's services stopped.
func TestRemoveCopiedSigningKeys_aFIFODoesNotHang(t *testing.T) {
	for _, where := range []string{"marker", "key"} {
		t.Run(where, func(t *testing.T) {
			f := newKeyFixture(t)
			f.write(t, f.copyOf("jwt-signing-key.pem"), "rsa")
			if err := os.MkdirAll(legacylayout.SecretsDir(f.oramaDir), 0o700); err != nil {
				t.Fatal(err)
			}
			fifo := f.original("jwt-signing-key.pem")
			if where == "marker" {
				fifo = legacylayout.CopiedKeysMarker(f.oramaDir)
			} else {
				f.record(t, map[string]string{"jwt-signing-key.pem": "rsa"})
			}
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- removeKeys(t, f.oramaDir) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("a FIFO must be refused")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the root step blocked on a FIFO")
			}
		})
	}
}
