package releaseverify

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/releaseverify/releasetest"
)

const fileTarget = "orama-linux-amd64.tar.gz"

// fileFixture is a node directory with an adopted root, a metadata
// directory and the archive the metadata names.
type fileFixture struct {
	repo  *releasetest.Repo
	check FileCheck
	path  string
	body  []byte
}

func newFileFixture(t *testing.T) *fileFixture {
	t.Helper()
	dir := t.TempDir()
	f := &fileFixture{repo: releasetest.NewRepo(t, testNow), body: []byte("release archive v1\n")}
	f.path = filepath.Join(dir, "archive.tar.gz")
	f.check = FileCheck{
		RootPath:    filepath.Join(dir, "release-root.json"),
		SeenPath:    filepath.Join(dir, "release-seen.json"),
		MetadataDir: filepath.Join(dir, "metadata"),
		Target:      fileTarget,
		Now:         testNow,
	}
	if err := os.Mkdir(f.check.MetadataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.repo.WriteRoot(t, f.check.RootPath)
	f.publish(t, f.check.MetadataDir, 1, time.Time{})
	if err := os.WriteFile(f.path, f.body, 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fileFixture) publish(t *testing.T, dir string, version int64, timestampExpires time.Time) {
	t.Helper()
	f.repo.Publish(t, dir, version, timestampExpires, map[string][]byte{fileTarget: f.body})
}

// run checks the archive through an open descriptor, as callers do.
func (f *fileFixture) run(t *testing.T, c FileCheck) (*Verified, error) {
	t.Helper()
	file, err := os.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	c.File = file
	return CheckFile(c)
}

func TestCheckFile_goodTargetRecordsTheSnapshot(t *testing.T) {
	f := newFileFixture(t)
	got, err := f.run(t, f.check)
	if err != nil {
		t.Fatal(err)
	}
	if got.SnapshotVersion != 1 {
		t.Fatalf("snapshot version %d", got.SnapshotVersion)
	}
	seen, err := readSeen(f.check.SeenPath)
	if err != nil || seen.SnapshotVersion != 1 {
		t.Fatalf("rollback record %+v, %v", seen, err)
	}
}

func TestCheckFile_tamperedFileIsRefused(t *testing.T) {
	f := newFileFixture(t)
	if err := os.WriteFile(f.path, []byte("release archive v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(t, f.check); !errors.Is(err, ErrTargetHash) {
		t.Fatalf("tampered archive: %v", err)
	}
	if _, err := os.Stat(f.check.SeenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused check wrote the rollback record: %v", err)
	}
}

func TestCheckFile_longerFileIsRefused(t *testing.T) {
	f := newFileFixture(t)
	if err := os.WriteFile(f.path, append(f.body, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(t, f.check); !errors.Is(err, ErrTargetHash) {
		t.Fatalf("appended archive: %v", err)
	}
}

func TestCheckFile_readsTheDescriptorFromItsStart(t *testing.T) {
	f := newFileFixture(t)
	file, err := os.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Seek(3, 0); err != nil {
		t.Fatal(err)
	}
	c := f.check
	c.File = file
	if _, err := CheckFile(c); err != nil {
		t.Fatalf("a descriptor that was read from was not rewound: %v", err)
	}
	c.File = nil
	if _, err := CheckFile(c); err == nil {
		t.Fatal("a check with no file passed")
	}
}

func TestCheckFile_expiredTimestampIsRefused(t *testing.T) {
	f := newFileFixture(t)
	f.publish(t, f.check.MetadataDir, 1, testNow.Add(-time.Minute))
	if _, err := f.run(t, f.check); !errors.Is(err, ErrFreeze) {
		t.Fatalf("expired timestamp: %v", err)
	}
}

func TestCheckFile_wrongRootIsRefused(t *testing.T) {
	f := newFileFixture(t)
	releasetest.NewRepo(t, testNow).WriteRoot(t, f.check.RootPath)
	if _, err := f.run(t, f.check); !errors.Is(err, ErrThreshold) {
		t.Fatalf("metadata signed under another root: %v", err)
	}
}

func TestCheckFile_noAdoptedRootIsRefused(t *testing.T) {
	f := newFileFixture(t)
	if err := os.Remove(f.check.RootPath); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(t, f.check); !errors.Is(err, ErrNoRoot) {
		t.Fatalf("no root: %v", err)
	}
}

func TestCheckFile_rollbackAcrossRunsIsRefused(t *testing.T) {
	f := newFileFixture(t)
	f.publish(t, f.check.MetadataDir, 2, time.Time{})
	if _, err := f.run(t, f.check); err != nil {
		t.Fatal(err)
	}
	f.publish(t, f.check.MetadataDir, 1, time.Time{})
	if _, err := f.run(t, f.check); !errors.Is(err, ErrRollback) {
		t.Fatalf("older snapshot after a newer one: %v", err)
	}
}

// TestCheckFile_concurrentChecksKeepTheHighestSnapshot races a check of
// version 1 against one of version 2. Whatever the order, the record must
// end at 2: without the lock both can read 0 and the version-1 write can
// land last.
func TestCheckFile_concurrentChecksKeepTheHighestSnapshot(t *testing.T) {
	const rounds = 20
	for i := 0; i < rounds; i++ {
		f := newFileFixture(t)
		newer := f.check
		newer.MetadataDir = t.TempDir()
		f.publish(t, newer.MetadataDir, 2, time.Time{})
		var wg sync.WaitGroup
		for _, c := range []FileCheck{f.check, newer} {
			wg.Add(1)
			go func(c FileCheck) {
				defer wg.Done()
				file, err := os.Open(f.path)
				if err != nil {
					t.Error(err)
					return
				}
				defer file.Close()
				c.File = file
				CheckFile(c)
			}(c)
		}
		wg.Wait()
		seen, err := readSeen(f.check.SeenPath)
		if err != nil || seen.SnapshotVersion != 2 {
			t.Fatalf("round %d: rollback record %+v, %v; want 2", i, seen, err)
		}
	}
}

func TestCheckFile_unknownTargetAndCorruptRecordAreRefused(t *testing.T) {
	f := newFileFixture(t)
	missing := f.check
	missing.Target = "other.tar.gz"
	if _, err := f.run(t, missing); err == nil {
		t.Fatal("a target the metadata does not name was accepted")
	}
	if err := os.WriteFile(f.check.SeenPath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(t, f.check); err == nil {
		t.Fatal("a corrupt rollback record was read as no record")
	}
}

func TestMatchOpen_refusesAnUnsupportedHash(t *testing.T) {
	f := newFileFixture(t)
	file, err := os.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	target := Target{Path: fileTarget, Length: int64(len(f.body)), Hashes: map[string][]byte{"md5": {1}}}
	if err := target.MatchOpen(file); !errors.Is(err, ErrTargetHash) {
		t.Fatalf("md5 target: %v", err)
	}
}
