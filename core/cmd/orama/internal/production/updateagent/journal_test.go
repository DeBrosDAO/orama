package updateagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestFileJournal_replaceWritesOverTheIntentThereAndCreatesTheDirectory(t *testing.T) {
	j := fileJournal{path: filepath.Join(t.TempDir(), "not", "yet", journalName)}
	first := autoupdate.Intent{Version: "0.3.1", Previous: "0.3.0"}
	if err := j.Replace(first); err != nil {
		t.Fatalf("replace with no intent there: %v", err)
	}
	second := first
	second.RollingBack, second.Blame = true, true
	if err := j.Replace(second); err != nil {
		t.Fatal(err)
	}
	got, err := j.Pending()
	if err != nil || got == nil || *got != second {
		t.Fatalf("pending = %+v, %v, want %+v", got, err, second)
	}
	info, err := os.Stat(j.path)
	if err != nil || info.Mode().Perm() != journalPerm {
		t.Fatalf("intent file: %v, %v", info, err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(j.path), "*"))
	if len(leftovers) != 1 {
		t.Fatalf("a replace left more than the intent behind: %v", leftovers)
	}
}

func TestFileJournal_replaceInADirectoryThatCannotBeWrittenIsAnError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	j := fileJournal{path: filepath.Join(blocker, journalName)}
	if err := j.Replace(autoupdate.Intent{Version: "1"}); err == nil {
		t.Fatal("an intent that could not be written was reported written")
	}
}

func TestFileJournal_beginRefusesAnIntentThatIsPendingAndKeepsIt(t *testing.T) {
	j := fileJournal{path: filepath.Join(t.TempDir(), journalName)}
	pending := autoupdate.Intent{Version: "0.3.1", Previous: "0.3.0", RollingBack: true}
	if err := j.Replace(pending); err != nil {
		t.Fatal(err)
	}
	err := j.Begin(autoupdate.Intent{Version: "0.3.2", Previous: "0.3.1"})
	if err == nil || !strings.Contains(err.Error(), "0.3.1") {
		t.Fatalf("err = %v, want one naming the unfinished install", err)
	}
	if got, _ := j.Pending(); got == nil || *got != pending {
		t.Fatalf("the unfinished intent was overwritten: %+v", got)
	}
}

func TestFileJournal_aSymlinkWhereTheIntentShouldBeIsNotFollowed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.json")
	if err := os.WriteFile(target, []byte(`{"version":"9.9.9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	j := fileJournal{path: filepath.Join(dir, journalName)}
	if err := os.Symlink(target, j.path); err != nil {
		t.Fatal(err)
	}
	if got, err := j.Pending(); err == nil || got != nil {
		t.Fatalf("pending = %+v, %v: a symlink was followed", got, err)
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
	m := &machine{restore: func(string) error { called = true; return nil }}
	if err := m.Restore(context.Background(), "0.3.0"); err != nil || !called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestPrintable_dropsWhatATerminalWouldActOn(t *testing.T) {
	if got := printable("refused\x1b[31m red ‮\nnext"); got != "refused[31m red next" {
		t.Fatalf("printable = %q", got)
	}
}

func TestMachineRecover_runsTheSwapRecovery(t *testing.T) {
	called := 0
	m := &machine{recover: func() error { called++; return nil }}
	if err := m.Recover(context.Background()); err != nil || called != 1 {
		t.Fatalf("called=%d err=%v", called, err)
	}
}

func TestMachineRecover_reportsAFailedRecovery(t *testing.T) {
	cause := errors.New("disk error")
	m := &machine{recover: func() error { return cause }}
	if err := m.Recover(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("err = %v", err)
	}
}

func TestMachineRestore_namesTheReleaseToGoBackTo(t *testing.T) {
	var got string
	m := &machine{restore: func(v string) error { got = v; return errors.New("refused") }}
	if err := m.Restore(context.Background(), "0.3.0"); err == nil || got != "0.3.0" {
		t.Fatalf("version %q, err %v", got, err)
	}
}
