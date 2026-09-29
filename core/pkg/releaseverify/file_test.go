package releaseverify

import (
	"errors"
	"os"
	"path/filepath"
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
	body  []byte
}

func newFileFixture(t *testing.T) *fileFixture {
	t.Helper()
	dir := t.TempDir()
	f := &fileFixture{repo: releasetest.NewRepo(t, testNow), body: []byte("release archive v1\n")}
	f.check = FileCheck{
		RootPath:    filepath.Join(dir, "release-root.json"),
		SeenPath:    filepath.Join(dir, "release-seen.json"),
		MetadataDir: filepath.Join(dir, "metadata"),
		Target:      fileTarget,
		File:        filepath.Join(dir, "archive.tar.gz"),
		Now:         testNow,
	}
	if err := os.Mkdir(f.check.MetadataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.repo.WriteRoot(t, f.check.RootPath)
	f.publish(t, 1, time.Time{})
	if err := os.WriteFile(f.check.File, f.body, 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fileFixture) publish(t *testing.T, version int64, timestampExpires time.Time) {
	t.Helper()
	f.repo.Publish(t, f.check.MetadataDir, version, timestampExpires, map[string][]byte{fileTarget: f.body})
}

func TestCheckFile_goodTargetRecordsTheSnapshot(t *testing.T) {
	f := newFileFixture(t)
	got, err := CheckFile(f.check)
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
	if err := os.WriteFile(f.check.File, []byte("release archive v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckFile(f.check); !errors.Is(err, ErrTargetHash) {
		t.Fatalf("tampered archive: %v", err)
	}
	if _, err := os.Stat(f.check.SeenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused check wrote the rollback record: %v", err)
	}
}

func TestCheckFile_longerFileIsRefused(t *testing.T) {
	f := newFileFixture(t)
	if err := os.WriteFile(f.check.File, append(f.body, 'x'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckFile(f.check); !errors.Is(err, ErrTargetHash) {
		t.Fatalf("appended archive: %v", err)
	}
}

func TestCheckFile_expiredTimestampIsRefused(t *testing.T) {
	f := newFileFixture(t)
	f.publish(t, 1, testNow.Add(-time.Minute))
	if _, err := CheckFile(f.check); !errors.Is(err, ErrFreeze) {
		t.Fatalf("expired timestamp: %v", err)
	}
}

func TestCheckFile_wrongRootIsRefused(t *testing.T) {
	f := newFileFixture(t)
	releasetest.NewRepo(t, testNow).WriteRoot(t, f.check.RootPath)
	_, err := CheckFile(f.check)
	if !errors.Is(err, ErrThreshold) {
		t.Fatalf("metadata signed under another root: %v", err)
	}
}

func TestCheckFile_noAdoptedRootIsRefused(t *testing.T) {
	f := newFileFixture(t)
	if err := os.Remove(f.check.RootPath); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckFile(f.check); !errors.Is(err, ErrNoRoot) {
		t.Fatalf("no root: %v", err)
	}
}

func TestCheckFile_rollbackAcrossRunsIsRefused(t *testing.T) {
	f := newFileFixture(t)
	f.publish(t, 2, time.Time{})
	if _, err := CheckFile(f.check); err != nil {
		t.Fatal(err)
	}
	f.publish(t, 1, time.Time{})
	if _, err := CheckFile(f.check); !errors.Is(err, ErrRollback) {
		t.Fatalf("older snapshot after a newer one: %v", err)
	}
}

func TestCheckFile_unknownTargetAndCorruptRecordAreRefused(t *testing.T) {
	f := newFileFixture(t)
	missing := f.check
	missing.Target = "other.tar.gz"
	if _, err := CheckFile(missing); err == nil {
		t.Fatal("a target the metadata does not name was accepted")
	}
	if err := os.WriteFile(f.check.SeenPath, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckFile(f.check); err == nil {
		t.Fatal("a corrupt rollback record was read as no record")
	}
}

func TestMatchFile_refusesAnUnsupportedHash(t *testing.T) {
	f := newFileFixture(t)
	target := Target{Path: fileTarget, Length: int64(len(f.body)), Hashes: map[string][]byte{"md5": {1}}}
	if err := target.MatchFile(f.check.File); !errors.Is(err, ErrTargetHash) {
		t.Fatalf("md5 target: %v", err)
	}
}
