package setup

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

func TestCheckReleaseFlags(t *testing.T) {
	good := Options{Release: "0.3.1", ReleaseRepo: "https://releases.example.org/tuf", ReleaseRoot: "root.json"}
	if err := checkReleaseFlags(good); err != nil {
		t.Fatalf("a complete release request: %v", err)
	}
	if err := checkReleaseFlags(Options{Archive: "a.tar.gz"}); err != nil {
		t.Fatalf("an archive request: %v", err)
	}
	for name, opts := range map[string]Options{
		"archive too":        {Release: "0.3.1", ReleaseRepo: good.ReleaseRepo, ReleaseRoot: "root.json", Archive: "a.tar.gz"},
		"no repo":            {Release: "0.3.1", ReleaseRoot: "root.json"},
		"no root":            {Release: "0.3.1", ReleaseRepo: good.ReleaseRepo},
		"plain http":         {Release: "0.3.1", ReleaseRepo: "http://releases.example.org", ReleaseRoot: "root.json"},
		"repo without it":    {ReleaseRepo: good.ReleaseRepo},
		"root without it":    {ReleaseRoot: "root.json"},
		"channel without it": {Channel: "nightly"},
	} {
		err := checkReleaseFlags(opts)
		var usage *clierr.Error
		if !errors.As(err, &usage) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
	}
}

// releaseServer publishes one archive on the stable channel at snapshot
// version, signed by a root that is written to the returned opts.
type releaseServer struct {
	keys    releaserepo.Keys
	rootOut string
	url     string
	dir     string
	archive []byte
}

func newReleaseServer(t *testing.T) *releaseServer {
	t.Helper()
	keys, err := releaserepo.GenerateKeys("stable", "nightly")
	if err != nil {
		t.Fatal(err)
	}
	root, err := releaserepo.NewRoot(keys, time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	s := &releaseServer{keys: keys, dir: t.TempDir(), archive: []byte("the release archive"), rootOut: filepath.Join(t.TempDir(), "root.json")}
	if err := os.WriteFile(s.rootOut, root, 0o644); err != nil {
		t.Fatal(err)
	}
	releaseverify.AllowLocalRepositories(t)
	srv := httptest.NewServer(http.FileServer(http.Dir(s.dir)))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// publish writes metadata at version, with the archive on disk as served
// (served differs from the bytes the metadata names when tampered).
func (s *releaseServer) publish(t *testing.T, version int64, timestampExpires time.Time, served []byte) {
	t.Helper()
	files, err := releaserepo.Build(s.keys, releaserepo.Spec{
		Version: version, RootValidUntil: time.Now().Add(24 * time.Hour), TimestampExpires: timestampExpires,
		Delegated:      []string{"stable", "nightly"},
		ChannelTargets: map[string]map[string][]byte{"stable": {releaseverify.ArchiveTarget("stable", "0.3.1", "amd64"): s.archive}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(s.dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(s.dir, "targets", "stable", "orama-0.3.1-linux-amd64.tar.gz")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, served, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (s *releaseServer) fetch(t *testing.T, seen string) (string, error) {
	t.Helper()
	opts := Options{Release: "0.3.1", ReleaseRepo: s.url, ReleaseRoot: s.rootOut}
	return fetchVerifiedRelease(context.Background(), t.TempDir(), opts, "stable", "amd64", seen)
}

func TestFetchVerifiedRelease_aGoodReleaseIsDownloadedAndRaisesTheRollbackRecord(t *testing.T) {
	s := newReleaseServer(t)
	s.publish(t, 5, time.Time{}, s.archive)
	seen := filepath.Join(t.TempDir(), "seen.json")
	path, err := s.fetch(t, seen)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(s.archive) {
		t.Fatalf("downloaded %q", got)
	}
	if got, _ := os.ReadFile(seen); !strings.Contains(string(got), "5") {
		t.Fatalf("rollback record %q", got)
	}
}

func TestFetchVerifiedRelease_aTamperedArchiveIsRefused(t *testing.T) {
	s := newReleaseServer(t)
	s.publish(t, 5, time.Time{}, []byte("the release archivE"))
	seen := filepath.Join(t.TempDir(), "seen.json")
	_, err := s.fetch(t, seen)
	if !errors.Is(err, releaseverify.ErrTargetHash) {
		t.Fatalf("err = %v, want a hash refusal", err)
	}
	if _, statErr := os.Stat(seen); statErr == nil {
		t.Fatal("a refused archive raised the rollback record")
	}
}

func TestFetchVerifiedRelease_aFrozenTimestampIsRefused(t *testing.T) {
	s := newReleaseServer(t)
	s.publish(t, 5, time.Now().Add(-time.Hour), s.archive)
	if _, err := s.fetch(t, filepath.Join(t.TempDir(), "seen.json")); !errors.Is(err, releaseverify.ErrFreeze) {
		t.Fatalf("err = %v, want a freeze", err)
	}
}

func TestFetchVerifiedRelease_anOlderSnapshotIsRefusedAfterANewerOne(t *testing.T) {
	s := newReleaseServer(t)
	seen := filepath.Join(t.TempDir(), "seen.json")
	s.publish(t, 6, time.Time{}, s.archive)
	if _, err := s.fetch(t, seen); err != nil {
		t.Fatal(err)
	}
	s.publish(t, 5, time.Time{}, s.archive)
	if _, err := s.fetch(t, seen); !errors.Is(err, releaseverify.ErrRollback) {
		t.Fatalf("err = %v, want a rollback", err)
	}
}

func TestFetchVerifiedRelease_metadataSignedByAnotherRootIsRefused(t *testing.T) {
	s := newReleaseServer(t)
	s.publish(t, 5, time.Time{}, s.archive)
	other := newReleaseServer(t)
	s.rootOut = other.rootOut
	if _, err := s.fetch(t, filepath.Join(t.TempDir(), "seen.json")); err == nil {
		t.Fatal("metadata under a root the operator did not name was accepted")
	}
}

func TestFetchVerifiedRelease_aVersionTheChannelDoesNotListIsRefused(t *testing.T) {
	s := newReleaseServer(t)
	s.publish(t, 5, time.Time{}, s.archive)
	opts := Options{Release: "9.9.9", ReleaseRepo: s.url, ReleaseRoot: s.rootOut}
	_, err := fetchVerifiedRelease(context.Background(), t.TempDir(), opts, "stable", "amd64", filepath.Join(t.TempDir(), "seen.json"))
	if err == nil || !strings.Contains(err.Error(), "does not name") {
		t.Fatalf("err = %v", err)
	}
}
