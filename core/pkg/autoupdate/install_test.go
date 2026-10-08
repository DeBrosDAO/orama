package autoupdate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestInstall_aGoodReleaseStagesUpgradesAndPassesTheGate(t *testing.T) {
	n := &fakeNode{current: "0.3.0"}
	res, err := Install(t.Context(), n, Release{Version: "0.3.1"})
	if err != nil || !res.Installed || res.ReleaseBad || res.RolledBack {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if want := []string{"stage", "upgrade", "healthy"}; !slices.Equal(n.calls, want) {
		t.Fatalf("calls %v", n.calls)
	}
}

func TestInstall_aFailedGateRestoresUpgradesAgainAndBlamesTheRelease(t *testing.T) {
	n := &fakeNode{current: "0.3.0", badRelease: true}
	res, err := Install(t.Context(), n, Release{Version: "0.3.1"})
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
	res, err := Install(t.Context(), n, Release{Version: "0.3.1"})
	if err == nil || res != (Result{}) {
		t.Fatalf("result %+v, err %v", res, err)
	}
	if want := []string{"stage"}; !slices.Equal(n.calls, want) {
		t.Fatalf("calls %v", n.calls)
	}
}

func TestInstall_anUpgradeThatStoppedNothingRestoresTheTreeAndDoesNotRestartAnything(t *testing.T) {
	n := &fakeNode{current: "0.3.0", upgradeErr: fmt.Errorf("a check refused: %w", ErrNotStarted)}
	res, err := Install(t.Context(), n, Release{Version: "0.3.1"})
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
	res, err := Install(t.Context(), n, Release{Version: "0.3.1"})
	if err == nil || res.RolledBack || !res.ReleaseBad {
		t.Fatalf("result %+v, err %v", res, err)
	}
	for _, want := range []string{"did not rejoin", "disk full"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q: %v", want, err)
		}
	}
}
