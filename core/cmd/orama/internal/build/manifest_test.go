package build

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifestArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return path
}

func TestReadArchiveManifest_ReturnsTheManifestBytes(t *testing.T) {
	const manifest = `{"version":"1.2.3","commit":"abc"}`
	path := writeManifestArchive(t, map[string]string{"bin/orama": "ELF", ManifestName: manifest})
	got, err := ReadArchiveManifest(path)
	if err != nil || string(got) != manifest {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestReadArchiveManifest_ArchiveWithoutManifestIsNamed(t *testing.T) {
	path := writeManifestArchive(t, map[string]string{"bin/orama": "ELF"})
	_, err := ReadArchiveManifest(path)
	if err == nil || !strings.Contains(err.Error(), "not an orama build archive") {
		t.Fatalf("expected a not-an-archive error, got %v", err)
	}
}

func TestReadArchiveManifest_NotGzipIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.tar.gz")
	os.WriteFile(path, []byte("plain text"), 0644)
	if _, err := ReadArchiveManifest(path); err == nil {
		t.Fatal("a non-gzip file must be an error")
	}
}

// The node's data lives under /opt/orama/.orama; clearing it on upload would
// wipe a node that is being re-set-up.
func TestArchiveOwnedPaths_NeverIncludesNodeData(t *testing.T) {
	for _, p := range ArchiveOwnedPaths {
		if p == "" || p == "." || strings.HasPrefix(p, ".orama") || strings.Contains(p, "/") {
			t.Errorf("archive-owned path %q could remove node data", p)
		}
	}
}
