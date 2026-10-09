package build

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

const releaseDate = "2026-10-01T00:00:00Z"

// unsignedRelease writes an unsigned release archive (what CI publishes) and
// returns its path. tamper changes a file after the manifest was written.
func unsignedRelease(t *testing.T, tamper bool) string {
	t.Helper()
	tree := t.TempDir()
	m := Manifest{Version: "0.3.1", Commit: "abc1234", Date: releaseDate, Arch: "amd64", Checksums: map[string]string{}}
	for rel, body := range map[string]string{"bin/orama": "cli", "bin/orama-node": "node", "systemd/x.service": "[Unit]\n"} {
		path := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(body))
		key := strings.TrimPrefix(rel, "bin/")
		m.Checksums[key] = hex.EncodeToString(sum[:])
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, archivetrust.ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if tamper {
		if err := os.WriteFile(filepath.Join(tree, "bin", "orama"), []byte("evil"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "release.tar.gz")
	built, err := time.Parse(dateLayout, releaseDate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeArchiveFile(out, tree, built, false); err != nil {
		t.Fatal(err)
	}
	return out
}

func releaseRoot(t *testing.T) []byte {
	t.Helper()
	keys, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	root, err := releaserepo.NewRoot(keys, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestEndorseRelease_theOperatorSignsTheReleaseAndItCarriesTheRoot(t *testing.T) {
	key, addr := newKey(t)
	agent := startFakeAgent(t, &fakeAgent{key: key, reported: addr})
	root := releaseRoot(t)
	out := filepath.Join(t.TempDir(), "endorsed.tar.gz")

	if err := endorseRelease(unsignedRelease(t, false), out, root, agent); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	if err := archivetrust.Extract(out, tree); err != nil {
		t.Fatal(err)
	}
	v, err := archivetrust.VerifyTree(tree, []string{strings.ToLower(addr)})
	if err != nil {
		t.Fatalf("the endorsed archive does not verify against the operator's wallet: %v", err)
	}
	if !bytes.Equal(v.ReleaseRoot, root) {
		t.Fatal("the endorsed archive does not carry the root it was endorsed under")
	}
	if v.Manifest.Version != "0.3.1" || v.Manifest.Date != releaseDate {
		t.Fatalf("the release's own manifest fields changed: %+v", v.Manifest)
	}
}

func TestEndorseRelease_aReleaseThatIsNotIntactIsNotSigned(t *testing.T) {
	key, addr := newKey(t)
	fake := &fakeAgent{key: key, reported: addr}
	agent := startFakeAgent(t, fake)
	out := filepath.Join(t.TempDir(), "endorsed.tar.gz")
	err := endorseRelease(unsignedRelease(t, true), out, releaseRoot(t), agent)
	if err == nil || !strings.Contains(err.Error(), "not intact") {
		t.Fatalf("err = %v", err)
	}
	if len(fake.signed) != 0 {
		t.Fatal("the operator's wallet signed a release that did not match its own manifest")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("an archive was written")
	}
}

func TestEndorseRelease_anArchiveThatIsAlreadySignedOrRotatingIsRefused(t *testing.T) {
	key, addr := newKey(t)
	agent := startFakeAgent(t, &fakeAgent{key: key, reported: addr})
	signedOnce := filepath.Join(t.TempDir(), "again.tar.gz")
	if err := endorseRelease(unsignedRelease(t, false), signedOnce, releaseRoot(t), agent); err != nil {
		t.Fatal(err)
	}
	// An endorsed archive carries a signature and a root; it is not a release.
	err := endorseRelease(signedOnce, filepath.Join(t.TempDir(), "x.tar.gz"), releaseRoot(t), agent)
	if err == nil {
		t.Fatal("an archive that already carries a signature and a root was endorsed as a release")
	}
}

func TestEndorseRelease_aLockedWalletFailsBeforeAnythingIsWritten(t *testing.T) {
	out := filepath.Join(t.TempDir(), "endorsed.tar.gz")
	err := endorseRelease(unsignedRelease(t, false), out, releaseRoot(t), unreachableAgent{})
	if err == nil {
		t.Fatal("endorsed without a wallet")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("an archive was written")
	}
}

// Both of two endorsements of one release are byte-identical: the archive is
// as reproducible as a build.
func TestEndorseRelease_sameInputsSameBytes(t *testing.T) {
	key, addr := newKey(t)
	agent := startFakeAgent(t, &fakeAgent{key: key, reported: addr})
	root := releaseRoot(t)
	release := unsignedRelease(t, false)
	var sums [2][32]byte
	for i := range sums {
		out := filepath.Join(t.TempDir(), "e.tar.gz")
		if err := endorseRelease(release, out, root, agent); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		sums[i] = sha256.Sum256(data)
	}
	if sums[0] != sums[1] {
		t.Fatal("two endorsements of one release differ")
	}
	if names := entryNames(t, release); strings.Join(names, ",") != "bin/,bin/orama,bin/orama-node,systemd/,systemd/x.service,manifest.json" {
		t.Fatalf("unsigned release entries: %v", names)
	}
}

func entryNames(t *testing.T, archive string) []string {
	t.Helper()
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
}
