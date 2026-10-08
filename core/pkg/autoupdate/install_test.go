package autoupdate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

var intent031 = Intent{Version: "0.3.1", Previous: "0.3.0"}

func TestInstall_aGoodReleaseStagesUpgradesAndPassesTheGate(t *testing.T) {
	n := &fakeNode{current: "0.3.0"}
	res, err := Install(t.Context(), n, &memJournal{}, intent031, Release{Version: "0.3.1"})
	if err != nil || !res.Installed || res.ReleaseBad || res.RolledBack {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if want := []string{"stage", "upgrade", "healthy"}; !slices.Equal(n.calls, want) {
		t.Fatalf("calls %v", n.calls)
	}
}

func TestInstall_aFailedGateRestoresUpgradesAgainAndBlamesTheRelease(t *testing.T) {
	n := &fakeNode{current: "0.3.0", badRelease: true}
	res, err := Install(t.Context(), n, &memJournal{}, intent031, Release{Version: "0.3.1"})
	if err == nil || res.Installed || !res.ReleaseBad || !res.RolledBack {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if want := []string{"stage", "upgrade", "healthy", "restore", "upgrade", "healthy"}; !slices.Equal(n.calls, want) {
		t.Fatalf("calls %v", n.calls)
	}
	if !strings.Contains(err.Error(), "back on its previous release") {
		t.Fatalf("the error does not say the node came back: %v", err)
	}
}

func TestInstall_aFailedStageChangesNothingAndBlamesNobody(t *testing.T) {
	n := &fakeNode{current: "0.3.0", stageErr: errors.New("refused")}
	res, err := Install(t.Context(), n, &memJournal{}, intent031, Release{Version: "0.3.1"})
	if err == nil || res != (Result{Unchanged: true}) || !res.Settled() {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if want := []string{"stage"}; !slices.Equal(n.calls, want) {
		t.Fatalf("calls %v", n.calls)
	}
}

func TestInstall_anUpgradeThatStoppedNothingRestoresTheTreeAndDoesNotRestartAnything(t *testing.T) {
	n := &fakeNode{current: "0.3.0", upgradeErr: fmt.Errorf("a check refused: %w", ErrNotStarted)}
	res, err := Install(t.Context(), n, &memJournal{}, intent031, Release{Version: "0.3.1"})
	if err == nil || res.ReleaseBad || !res.RolledBack {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if want := []string{"stage", "upgrade", "restore", "healthy"}; !slices.Equal(n.calls, want) {
		t.Fatalf("calls %v", n.calls)
	}
}

// brokenRestore cannot put the previous release back.
type brokenRestore struct{ fakeNode }

func (b *brokenRestore) Restore(context.Context) error { return errors.New("disk full") }

func TestInstall_aRestoreThatFailsIsReportedWithTheOriginalFailure(t *testing.T) {
	n := &brokenRestore{fakeNode{current: "0.3.0", badRelease: true}}
	res, err := Install(t.Context(), n, &memJournal{}, intent031, Release{Version: "0.3.1"})
	if err == nil || res.RolledBack || !res.ReleaseBad {
		t.Fatalf("result %+v, err %v", res, err)
	}
	for _, want := range []string{"did not rejoin", "disk full"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q: %v", want, err)
		}
	}
}

// A run that is stopped (SIGTERM, the service's timeout) did not find the
// release bad: nothing is rolled back and nothing is blamed, and the next run
// finishes the install.
func TestFinish_aStoppedRunRollsNothingBackAndBlamesNobody(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	n := &stoppedNode{fakeNode: fakeNode{current: "0.3.1"}, stop: cancel}
	res, err := Finish(ctx, n, &memJournal{}, intent031)
	if err == nil || res.ReleaseBad || res.Settled() {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "the next run finishes it") {
		t.Fatalf("err = %v", err)
	}
	for _, call := range n.calls {
		if call == "restore" {
			t.Fatalf("a stopped run rolled back: %v", n.calls)
		}
	}
}

// stoppedNode is stopped during its upgrade.
type stoppedNode struct {
	fakeNode
	stop context.CancelFunc
}

func (s *stoppedNode) Upgrade(ctx context.Context) error {
	s.calls = append(s.calls, "upgrade")
	s.stop()
	return ctx.Err()
}

func TestFinish_aRestoreThatFailsLeavesTheResultUnsettled(t *testing.T) {
	n := &fakeNode{current: "0.3.1", previous: "0.3.0", badRelease: true, restoreErr: errors.New("disk full")}
	res, err := Finish(t.Context(), n, &memJournal{}, intent031)
	if err == nil || res.Settled() || !res.ReleaseBad {
		t.Fatalf("result %+v, err %v", res, err)
	}
}

// A step after the swap can fail with the release already in place: the node is
// not unchanged, and the health gate judges it.
func TestInstall_aStageThatFailedAfterTheSwapGoesOnToTheGate(t *testing.T) {
	n := &fakeNode{current: "0.3.0", stageErr: errors.New("sync failed"), stagedThenFailed: true}
	res, err := Install(t.Context(), n, &memJournal{}, intent031, Release{Version: "0.3.1"})
	if err != nil || !res.Installed || res.Unchanged {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if want := []string{"stage", "upgrade", "healthy"}; !slices.Equal(n.calls, want) {
		t.Fatalf("calls %v", n.calls)
	}
}

// The rollback is journaled before it begins, so a kill in the middle of it is
// finished by the next run.
func TestFinish_aRollbackIsJournaledBeforeItBegins(t *testing.T) {
	j := &memJournal{}
	n := &fakeNode{current: "0.3.1", previous: "0.3.0", badRelease: true, restoreKilled: true}
	_, _ = Finish(t.Context(), n, j, intent031)
	if j.intent == nil || !j.intent.RollingBack || !j.intent.Blame {
		t.Fatalf("intent %+v", j.intent)
	}
}
