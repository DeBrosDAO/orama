package updateagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

func TestFileJournal_beginPendingClear(t *testing.T) {
	j := fileJournal{path: filepath.Join(t.TempDir(), "work", journalName)}
	if got, err := j.Pending(); got != nil || err != nil {
		t.Fatalf("nothing begun: %v, %v", got, err)
	}
	want := autoupdate.Intent{Version: "0.3.1", Previous: "0.3.0", StartedAt: time.Unix(1_700_000_000, 0).UTC()}
	if err := j.Begin(want); err != nil {
		t.Fatal(err)
	}
	got, err := j.Pending()
	if err != nil || got == nil || *got != want {
		t.Fatalf("pending = %+v, %v", got, err)
	}
	info, err := os.Stat(j.path)
	if err != nil || info.Mode().Perm() != journalPerm {
		t.Fatalf("intent file: %v, %v", info, err)
	}
	if err := j.Begin(want); err == nil {
		t.Fatal("a second install began over an unfinished one")
	}
	if err := j.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := j.Clear(); err != nil {
		t.Fatalf("clearing nothing: %v", err)
	}
	if got, _ := j.Pending(); got != nil {
		t.Fatal("a cleared intent is still pending")
	}
}

func TestFileJournal_aCorruptIntentIsAnErrorAndNotNoIntent(t *testing.T) {
	j := fileJournal{path: filepath.Join(t.TempDir(), journalName)}
	if err := os.WriteFile(j.path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := j.Pending(); err == nil || got != nil {
		t.Fatalf("pending = %v, %v: a corrupt intent read as none would hide an unfinished install", got, err)
	}
	if err := j.Begin(autoupdate.Intent{Version: "1"}); err == nil {
		t.Fatal("an install began over an intent that cannot be read")
	}
}

func TestLockRun_oneRunAtATime(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockRun(dir); !errors.Is(err, errRunning) {
		t.Fatalf("a second run: %v, want errRunning", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	again, err := lockRun(dir)
	if err != nil {
		t.Fatalf("the lock was not freed: %v", err)
	}
	_ = again()
}

func TestMachineStage_isAReleaseOnlyStageThatKeepsThePreviousRelease(t *testing.T) {
	var got push.StageOptions
	m := &machine{stage: func(o push.StageOptions) error { got = o; return errors.New("refused") }}
	rel := autoupdate.Release{Dir: "/var/lib/orama-autoupdate/fetch-1", Target: releaseverify.Target{Path: "stable/orama-0.3.1-linux-amd64.tar.gz"}}
	if err := m.Stage(context.Background(), rel); err == nil {
		t.Fatal("a stage that failed was reported as done")
	}
	want := push.StageOptions{
		Archive: rel.ArchivePath(), ReleaseMetadata: rel.MetadataDir(), ReleaseTarget: rel.Target.Path,
		ReleaseOnly: true, KeepPrevious: true,
	}
	if got.Archive != want.Archive || got.ReleaseMetadata != want.ReleaseMetadata || got.ReleaseTarget != want.ReleaseTarget ||
		!got.ReleaseOnly || !got.KeepPrevious || len(got.TrustSigners) != 0 {
		t.Fatalf("stage options = %+v, want %+v", got, want)
	}
}

func TestMachineRestore_putsThePreviousReleaseBack(t *testing.T) {
	called := false
	m := &machine{restore: func() error { called = true; return nil }}
	if err := m.Restore(context.Background()); err != nil || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestPrintable_dropsWhatATerminalWouldActOn(t *testing.T) {
	if got := printable("refused\x1b[31m red ‮\nnext"); got != "refused[31m red next" {
		t.Fatalf("printable = %q", got)
	}
}
