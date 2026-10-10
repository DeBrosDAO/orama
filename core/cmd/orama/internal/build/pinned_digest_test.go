package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// oramaDigest is a well-formed digest that is not the digest of any test body.
const oramaDigest = "1b6b4a2cb8cf4d1c6c4a6d2a1d2c3f0e63ca4a6cb3c6d0ff2d6c0d0f1a6e7f5b"

func writeTarball(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.tar.gz")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerifyPinnedSHA256_matchingDigestPasses(t *testing.T) {
	p := writeTarball(t, "orama")
	sum, err := sha256File(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPinnedSHA256(p, "x.tar.gz", "amd64", map[string]string{"amd64": sum}); err != nil {
		t.Fatalf("a tarball with the pinned digest was refused: %v", err)
	}
}

func TestVerifyPinnedSHA256_differentBytesAreRefused(t *testing.T) {
	p := writeTarball(t, "tampered")
	err := verifyPinnedSHA256(p, "x.tar.gz", "amd64", map[string]string{"amd64": oramaDigest})
	if err == nil || !strings.Contains(err.Error(), "not the pinned") {
		t.Fatalf("a tarball with another digest was accepted: %v", err)
	}
}

func TestVerifyPinnedSHA256_noPinForTheArchIsRefused(t *testing.T) {
	p := writeTarball(t, "orama")
	if err := verifyPinnedSHA256(p, "x.tar.gz", "riscv64", map[string]string{"amd64": oramaDigest}); err == nil {
		t.Fatal("an architecture with no pinned digest was accepted")
	}
}

func TestVerifyPinnedSHA256_missingFileIsAnError(t *testing.T) {
	if err := verifyPinnedSHA256(filepath.Join(t.TempDir(), "absent"), "x", "amd64", map[string]string{"amd64": oramaDigest}); err == nil {
		t.Fatal("a missing file was accepted")
	}
}

// Every architecture the build targets has a pinned digest of a SHA-256's length.
func TestReleaseDigests_coverTheBuildArchitectures(t *testing.T) {
	for name, pins := range map[string]map[string]string{"kubo": constants.IPFSKuboTarballSHA256, "rqlite": constants.RQLiteTarballSHA256} {
		for _, arch := range []string{"amd64", "arm64"} {
			if len(pins[arch]) != 64 {
				t.Errorf("%s has no 64-hex digest pinned for %s: %q", name, arch, pins[arch])
			}
		}
	}
}
