package releaseverify

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAccept_raisesTheRollbackRecordWithoutTheFile(t *testing.T) {
	f := newFileFixture(t)
	f.publish(t, f.check.MetadataDir, 4, time.Time{})
	got, err := Accept(f.check)
	if err != nil {
		t.Fatal(err)
	}
	if got.SnapshotVersion != 4 {
		t.Fatalf("snapshot version %d", got.SnapshotVersion)
	}
	seen, err := readSeen(f.check.SeenPath)
	if err != nil || seen.SnapshotVersion != 4 {
		t.Fatalf("rollback record %+v, %v", seen, err)
	}
	f.publish(t, f.check.MetadataDir, 3, time.Time{})
	if _, err := Accept(f.check); !errors.Is(err, ErrRollback) {
		t.Fatalf("an older snapshot after an accepted one: %v", err)
	}
}

func TestAccept_aTargetTheMetadataDoesNotNameWritesNothing(t *testing.T) {
	f := newFileFixture(t)
	c := f.check
	c.Target = "orama-linux-arm64.tar.gz"
	if _, err := Accept(c); err == nil || !strings.Contains(err.Error(), "does not name") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(f.check.SeenPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused acceptance wrote the rollback record: %v", err)
	}
}

func TestAccept_expiredMetadataIsRefused(t *testing.T) {
	f := newFileFixture(t)
	f.publish(t, f.check.MetadataDir, 2, testNow.Add(-time.Hour))
	if _, err := Accept(f.check); err == nil {
		t.Fatal("metadata past its expiry was accepted")
	}
}
