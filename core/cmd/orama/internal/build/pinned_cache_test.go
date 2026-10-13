package build

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func pinFor(t *testing.T, body []byte) map[string]string {
	t.Helper()
	sum := sha256.Sum256(body)
	return map[string]string{"amd64": hex.EncodeToString(sum[:])}
}

// TestFetchPinned_usesTheCacheAfterTheFirstDownload: every build downloaded
// the pinned tarballs again, and a network blip failed the deploy.
func TestFetchPinned_usesTheCacheAfterTheFirstDownload(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	body := []byte("release tarball")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Write(body)
	}))
	pins := pinFor(t, body)
	dir := t.TempDir()
	if err := fetchPinned(srv.URL, filepath.Join(dir, "a.tgz"), "a.tgz", "amd64", pins); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	if err := fetchPinned(srv.URL, filepath.Join(dir, "b.tgz"), "b.tgz", "amd64", pins); err != nil {
		t.Fatalf("the second fetch needed the network: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("downloaded %d times, want 1", hits.Load())
	}
	got, _ := os.ReadFile(filepath.Join(dir, "b.tgz"))
	if string(got) != string(body) {
		t.Errorf("cached copy is %q", got)
	}
}

func TestFetchPinned_aCorruptCacheEntryIsReplaced(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	body := []byte("the real tarball")
	pins := pinFor(t, body)
	cached, err := pinnedCachePath(pins["amd64"])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cached), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cached, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(body) }))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "x.tgz")
	if err := fetchPinned(srv.URL, dest, "x.tgz", "amd64", pins); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(cached); string(got) != string(body) {
		t.Errorf("the corrupt cache entry was not replaced: %q", got)
	}
}

func TestFetchPinned_aDownloadThatIsNotThePinnedReleaseIsRefusedAndNotCached(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	pins := pinFor(t, []byte("the pinned release"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("something else")) }))
	defer srv.Close()
	if err := fetchPinned(srv.URL, filepath.Join(t.TempDir(), "x.tgz"), "x.tgz", "amd64", pins); err == nil {
		t.Fatal("a tarball that is not the pinned release was accepted")
	}
	cached, _ := pinnedCachePath(pins["amd64"])
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Errorf("a wrong tarball was cached: %v", err)
	}
	if err := fetchPinned(srv.URL, filepath.Join(t.TempDir(), "y.tgz"), "y.tgz", "arm64", pins); err == nil {
		t.Error("an architecture with no pin was accepted")
	}
}
