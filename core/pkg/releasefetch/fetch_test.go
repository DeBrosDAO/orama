package releasefetch

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

var now = time.Now()

// repo is a served release repository under a generated root.
type repo struct {
	keys    releaserepo.Keys
	root    []byte
	dir     string
	url     string
	archive []byte
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	keys, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	root, err := releaserepo.NewRoot(keys, now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	r := &repo{keys: keys, root: root, dir: t.TempDir(), archive: []byte("nightly archive bytes")}
	releaseverify.AllowLocalRepositories(t)
	srv := httptest.NewServer(http.FileServer(http.Dir(r.dir)))
	t.Cleanup(srv.Close)
	r.url = srv.URL
	r.publish(t, 3, "0.3.1", r.archive, r.archive)
	return r
}

// publish lists archive as nightly version and serves served as its bytes.
func (r *repo) publish(t *testing.T, snapshot int64, version string, listed, served []byte) {
	t.Helper()
	files, err := releaserepo.Build(r.keys, releaserepo.Spec{
		Version: snapshot, RootValidUntil: now.Add(24 * time.Hour),
		Targets: map[string][]byte{releaseverify.ArchiveTarget("nightly", version, "amd64"): listed},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(r.dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(r.dir, "targets", "nightly", "orama-"+version+"-linux-amd64.tar.gz")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, served, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *repo) params(t *testing.T) Params {
	t.Helper()
	return Params{
		RepoURL: r.url, Channel: "nightly", Arch: "amd64", Root: r.root, RootSHA256: releaseverify.RootDigest(r.root),
		WorkDir: filepath.Join(t.TempDir(), "work"), SeenPath: filepath.Join(t.TempDir(), "seen.json"),
		AdoptedRoot: filepath.Join(t.TempDir(), "adopted-root.json"), Now: now,
	}
}

func TestFetch_downloadsAndVerifiesTheNewestRelease(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)

	rel, err := Fetch(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.3.1" || rel.Target != "nightly/orama-0.3.1-linux-amd64.tar.gz" {
		t.Fatalf("release = %+v", rel)
	}
	got, err := os.ReadFile(rel.ArchivePath)
	if err != nil || string(got) != string(r.archive) {
		t.Fatalf("archive %q, %v", got, err)
	}
	for _, name := range []string{"timestamp.json", "snapshot.json", "targets.json"} {
		if _, err := os.Stat(filepath.Join(rel.MetadataDir, name)); err != nil {
			t.Errorf("metadata lacks %s: %v", name, err)
		}
	}
	if string(rel.Root) != string(r.root) {
		t.Error("the release root changed without a rotation")
	}
	if err := rel.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rel.ArchivePath); err == nil {
		t.Fatal("Remove left the archive")
	}
}

func TestFetch_theRootMustBeTheOneTheNetworkPins(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	p.RootSHA256 = strings.Repeat("ab", 32)
	if _, err := Fetch(t.Context(), p); err == nil || !strings.Contains(err.Error(), "pins") {
		t.Fatalf("err = %v", err)
	}
	other := newRepo(t)
	p = r.params(t)
	p.Root = other.root
	if _, err := Fetch(t.Context(), p); err == nil {
		t.Fatal("a root other than the pinned one was used")
	}
	for _, bad := range []string{"", "xyz", strings.Repeat("a", 63)} {
		p = r.params(t)
		p.RootSHA256 = bad
		if _, err := Fetch(t.Context(), p); err == nil {
			t.Errorf("pin %q accepted", bad)
		}
	}
}

func TestFetch_followsAPublishedRootRotationFromTheEmbeddedRoot(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	next, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	second, err := releaserepo.NextRoot(r.root, r.keys, next, now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "2.root.json"), second, 0o644); err != nil {
		t.Fatal(err)
	}
	r.keys = next
	r.publish(t, 4, "0.3.2", r.archive, r.archive)

	rel, err := Fetch(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "0.3.2" || string(rel.Root) != string(second) {
		t.Fatalf("version %s, rotated root adopted: %v", rel.Version, string(rel.Root) == string(second))
	}
}

// The embedded root may have expired by the time a CLI is used; the rotation
// that renewed it is what makes the repository usable.
func TestFetch_anExpiredEmbeddedRootIsRenewedByTheRepository(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	second, err := releaserepo.NextRoot(r.root, r.keys, r.keys, now.Add(72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "2.root.json"), second, 0o644); err != nil {
		t.Fatal(err)
	}
	p.Now = now.Add(36 * time.Hour)
	// The metadata expires with the first root; publish it again under the second.
	files, err := releaserepo.Build(r.keys, releaserepo.Spec{
		Version: 5, RootValidUntil: now.Add(72 * time.Hour),
		Targets: map[string][]byte{releaseverify.ArchiveTarget("nightly", "0.3.1", "amd64"): r.archive},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(r.dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Fetch(t.Context(), p); err != nil {
		t.Fatalf("a renewed root was not followed: %v", err)
	}
}

func TestFetch_anArchiveThatIsNotTheSignedOneIsRefused(t *testing.T) {
	r := newRepo(t)
	r.publish(t, 4, "0.3.1", r.archive, []byte("tampered archive bytes!"))
	if _, err := Fetch(t.Context(), r.params(t)); err == nil {
		t.Fatal("an archive that does not match the signed hashes was accepted")
	}
}

func TestFetch_aChannelWithNothingForTheArchitectureIsAnError(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	p.Arch = "arm64"
	if _, err := Fetch(t.Context(), p); err == nil || !strings.Contains(err.Error(), "lists no release for linux/arm64") {
		t.Fatalf("err = %v", err)
	}
	p = r.params(t)
	p.Channel = "main"
	if _, err := Fetch(t.Context(), p); err == nil {
		t.Fatal("an empty channel yielded a release")
	}
}

func TestFetch_minVersion(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	p.MinVersion = "0.3.1"
	if _, err := Fetch(t.Context(), p); err != nil {
		t.Fatalf("a release at the minimum: %v", err)
	}
	p = r.params(t)
	p.MinVersion = "0.4.0"
	if _, err := Fetch(t.Context(), p); err == nil || !strings.Contains(err.Error(), "older than the 0.4.0") {
		t.Fatalf("err = %v", err)
	}
	p.MinVersion = "not-a-version"
	if _, err := Fetch(t.Context(), p); err == nil {
		t.Fatal("a minimum that cannot be compared was ignored")
	}
}

func TestFetch_aReplayedOlderSnapshotIsRefused(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	if _, err := Fetch(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	r.publish(t, 2, "0.3.1", r.archive, r.archive)
	p.WorkDir = filepath.Join(t.TempDir(), "again")
	if _, err := Fetch(t.Context(), p); err == nil {
		t.Fatal("a snapshot older than the one already accepted was taken")
	}
}

func TestFetch_badParametersAreRefusedBeforeTheNetwork(t *testing.T) {
	r := newRepo(t)
	cases := map[string]func(p *Params){
		"a private repository": func(p *Params) { p.RepoURL = "https://10.0.0.1/releases" },
		"a bad channel":        func(p *Params) { p.Channel = "Nightly" },
		"an unknown arch":      func(p *Params) { p.Arch = "riscv64" },
		"no work directory":    func(p *Params) { p.WorkDir = "" },
		"no rollback record":   func(p *Params) { p.SeenPath = "" },
		"no clock":             func(p *Params) { p.Now = time.Time{} },
		"no root":              func(p *Params) { p.Root = nil },
	}
	for name, change := range cases {
		p := r.params(t)
		change(&p)
		releaseverify.AllowLocalRepositories(t)
		if name == "a private repository" {
			t.Setenv(releaseverify.AllowLocalEnv, "")
		}
		if _, err := Fetch(t.Context(), p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A second fetch walks the same rotation again from the pinned root; it must
// not clear the rollback record the first one raised, or a replayed snapshot
// would be taken.
func TestFetch_aRotationAlreadyTakenDoesNotClearTheRollbackRecordAgain(t *testing.T) {
	r := newRepo(t)
	next, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	second, err := releaserepo.NextRoot(r.root, r.keys, next, now.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "2.root.json"), second, 0o644); err != nil {
		t.Fatal(err)
	}
	r.keys = next
	r.publish(t, 9, "0.3.2", r.archive, r.archive)
	p := r.params(t)
	if _, err := Fetch(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	// The same repository replays an older snapshot.
	r.publish(t, 4, "0.3.2", r.archive, r.archive)
	p.WorkDir = filepath.Join(t.TempDir(), "again")
	if _, err := Fetch(t.Context(), p); err == nil {
		t.Fatal("a snapshot older than the one accepted was taken after the same rotation was walked again")
	}
}

func TestFetch_aSymlinkLeftInTheWorkDirectoryIsNotWrittenThrough(t *testing.T) {
	r := newRepo(t)
	p := r.params(t)
	if err := os.MkdirAll(p.WorkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(p.WorkDir, "release-root.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Fetch(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "keep" {
		t.Fatalf("the symlink target was overwritten: %q", got)
	}
}
