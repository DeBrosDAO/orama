// Package releasetest builds TUF repositories for tests. Every key is
// generated when a test runs; nothing here is a production root, and no
// root this package makes is ever written outside a test's temp directory.
// The metadata itself is made by releaserepo.
package releasetest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify/releaserepo"
)

// Metadata file names, as releaseverify reads them from a directory.
const (
	RootFile      = releaserepo.RootFile
	TimestampFile = releaserepo.TimestampFile
	SnapshotFile  = releaserepo.SnapshotFile
	TargetsFile   = releaserepo.TargetsFile
)

// validity is how long a generated root and its roles stay unexpired
// after the reference time.
const validity = 7 * 24 * time.Hour

// Repo is a generated root with one key per top-level role.
type Repo struct {
	root       []byte
	keys       releaserepo.Keys
	validUntil time.Time
}

// NewRepo generates a root valid for a week after now.
func NewRepo(t testing.TB, now time.Time) *Repo {
	t.Helper()
	keys, err := releaserepo.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	r := &Repo{keys: keys, validUntil: now.Add(validity)}
	if r.root, err = releaserepo.NewRoot(keys, r.validUntil); err != nil {
		t.Fatal(err)
	}
	return r
}

// Root is the signed root.json.
func (r *Repo) Root() []byte { return append([]byte(nil), r.root...) }

// WriteRoot writes root.json to path, as a node's adopted root.
func (r *Repo) WriteRoot(t testing.TB, path string) {
	t.Helper()
	if err := os.WriteFile(path, r.root, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Publish writes timestamp, snapshot and targets metadata naming targets
// into dir, at version. A zero timestampExpires is the root's own expiry;
// one before the reference time is a frozen repository.
func (r *Repo) Publish(t testing.TB, dir string, version int64, timestampExpires time.Time, targets map[string][]byte) {
	t.Helper()
	files, err := releaserepo.Build(r.keys, releaserepo.Spec{
		Version: version, RootValidUntil: r.validUntil, TimestampExpires: timestampExpires, Targets: targets,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
