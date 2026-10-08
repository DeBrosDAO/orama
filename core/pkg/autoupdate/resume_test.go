package autoupdate

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// The journal is written before the release is staged and gone once the node is
// settled.
func TestAgent_theJournalFollowsAnInstall(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	began := false
	h.agent.Journal = &watchJournal{memJournal: h.jrnl, onBegin: func(i Intent) {
		began = i.Version == testVersion && i.Previous == "0.3.0" && !i.StartedAt.IsZero()
	}}
	if out, err := h.run(t); err != nil || out.Action != OutcomeInstalled {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if !began {
		t.Fatal("the intent was not recorded before the install")
	}
	if h.jrnl.intent != nil {
		t.Fatalf("an installed release left an intent: %+v", h.jrnl.intent)
	}
}

// watchJournal reports Begin to the test.
type watchJournal struct {
	*memJournal
	onBegin func(Intent)
}

func (w *watchJournal) Begin(i Intent) error {
	w.onBegin(i)
	return w.memJournal.Begin(i)
}

func TestAgent_theJournalIsClearedWhenNothingChangedOrTheNodeWasPutBack(t *testing.T) {
	for name, prepare := range map[string]func(*harness){
		"a stage that failed":        func(h *harness) { h.node.stageErr = fmt.Errorf("refused") },
		"a rollback after a failure": func(h *harness) { h.node.badRelease = true },
		"a refused upgrade":          func(h *harness) { h.node.upgradeErr = fmt.Errorf("a check refused: %w", ErrNotStarted) },
	} {
		t.Run(name, func(t *testing.T) {
			db, rel := newClusterDB(t), newRelease(t)
			h := newHarness(t, db, rel, "10.0.0.2")
			auto(t, h)
			prepare(h)
			_, _ = h.run(t)
			if h.jrnl.intent != nil {
				t.Fatalf("an intent was left for a node that is settled: %+v", h.jrnl.intent)
			}
		})
	}
}

func TestAgent_aRestoreThatFailedKeepsTheJournalSoTheNextRunRepairsTheNode(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.badRelease = true
	h.node.restoreErr = fmt.Errorf("disk full")
	if _, err := h.run(t); err == nil {
		t.Fatal("a node that could not be put back was reported fine")
	}
	if h.jrnl.intent == nil {
		t.Fatal("the intent was cleared for a node that is not settled")
	}
}

// A run killed between staging and the end of the upgrade leaves the new release
// under /opt/orama and an intent. The next run finishes the install; it does
// not conclude from the node's manifest that there is nothing to install.
func TestAgent_aRunKilledMidInstallIsFinishedByTheNext(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.current = testVersion // staged
	h.jrnl.intent = &Intent{Version: testVersion, Previous: "0.3.0", StartedAt: time.Now()}

	out, err := h.run(t)
	if err != nil || out.Action != OutcomeInstalled || out.Version != testVersion {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if want := []string{"recover", "upgrade", "healthy"}; !slices.Equal(h.node.calls, want) {
		t.Fatalf("the node was asked %v, want %v", h.node.calls, want)
	}
	if got := installState(t, db, testVersion, "n2"); got != StateInstalled {
		t.Fatalf("n2 recorded %q", got)
	}
	if h.jrnl.intent != nil {
		t.Fatal("the intent survived the finished install")
	}
}

func TestAgent_aResumedInstallThatFailsIsPutBackAndMarkedBad(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.current, h.node.previous, h.node.badRelease = testVersion, "0.3.0", true
	h.jrnl.intent = &Intent{Version: testVersion, Previous: "0.3.0", StartedAt: time.Now()}
	out, err := h.run(t)
	if err == nil || out.Action != OutcomeFailed {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if got := installState(t, db, testVersion, "n2"); got != StateFailed {
		t.Fatalf("n2 recorded %q", got)
	}
	if h.node.current != "0.3.0" || h.jrnl.intent != nil {
		t.Fatalf("node on %s, intent %+v", h.node.current, h.jrnl.intent)
	}
}

// Updates being turned off does not leave a half-installed node half-installed.
func TestAgent_anInstallIsFinishedEvenIfTheClusterTurnedUpdatesOff(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	h.node.current = testVersion
	h.jrnl.intent = &Intent{Version: testVersion, Previous: "0.3.0", StartedAt: time.Now()}
	setSetting(t, db, "auto_update", "off")
	if out, err := h.run(t); err != nil || out.Action != OutcomeInstalled {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
}

// An intent whose release is not what the node runs is a stage that never
// completed or was undone: it is discarded, and the run goes on as any other.
func TestAgent_aStaleIntentIsDiscarded(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	h.jrnl.intent = &Intent{Version: "0.9.9", Previous: "0.3.0", StartedAt: time.Now()}
	out, err := h.run(t)
	if err != nil || out.Action != ActionNotify {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if h.jrnl.intent != nil {
		t.Fatalf("a stale intent survived: %+v", h.jrnl.intent)
	}
	if want := []string{"recover"}; !slices.Equal(h.node.calls, want) {
		t.Fatalf("a stale intent made the node do something: %v", h.node.calls)
	}
}

func TestAgent_aResumeWaitsForTheLockAnotherNodeHolds(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	free, err := testStore(db).Lock(t.Context(), "n3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = free(t.Context()) }()
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.current = testVersion
	h.jrnl.intent = &Intent{Version: testVersion, Previous: "0.3.0", StartedAt: time.Now()}
	out, err := h.run(t)
	if err != nil || out.Action != OutcomeWait || !strings.Contains(out.Reason, "lock") {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if !slices.Equal(h.node.calls, []string{"recover"}) || h.jrnl.intent == nil {
		t.Fatalf("calls %v, intent %+v: the install must wait, not give up", h.node.calls, h.jrnl.intent)
	}
}

// A node that already runs the release (pushed by hand, installed at that
// version) says so, so that it does not hold the rollout at its place in the
// plan for every node after it.
func TestAgent_aNodeAlreadyOnTheReleaseDoesNotStallTheRollout(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	first := newHarness(t, db, rel, "10.0.0.2") // first in the plan
	first.node.current = testVersion
	auto(t, first)
	if out, err := first.run(t); err != nil || out.Action != ActionNone {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if got := installState(t, db, testVersion, "n2"); got != StateInstalled {
		t.Fatalf("n2 recorded %q", got)
	}
	second := newHarness(t, db, rel, "10.0.0.3")
	auto(t, second)
	if out, err := second.run(t); err != nil || out.Action != OutcomeInstalled {
		t.Fatalf("the next node: %+v, %v", out, err)
	}
}

func TestAgent_aNodeOnTheReleaseRecordsNothingOnNotify(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	h.node.current = testVersion
	if _, err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if got := installState(t, db, testVersion, "n2"); got != "" {
		t.Fatalf("notify wrote %q to the cluster's registry", got)
	}
}

// A run killed between the two steps of a swap leaves a tree whose manifest
// cannot be read. The next run recovers it before it compares versions; without
// that it would take the empty version for a stale intent and drop it.
func TestAgent_aHalfSwappedTreeIsRecoveredBeforeTheIntentIsJudged(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.current, h.node.unreadable = testVersion, true
	h.jrnl.intent = &Intent{Version: testVersion, Previous: "0.3.0", StartedAt: time.Now()}
	out, err := h.run(t)
	if err != nil || out.Action != OutcomeInstalled {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if len(h.node.calls) == 0 || h.node.calls[0] != "recover" {
		t.Fatalf("calls %v: the tree was not recovered first", h.node.calls)
	}
}

func TestAgent_anUnreadableTreeThatCannotBeRecoveredKeepsTheIntent(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.unreadable = true
	h.node.recoverErr = fmt.Errorf("disk error")
	h.jrnl.intent = &Intent{Version: testVersion, Previous: "0.3.0", StartedAt: time.Now()}
	if _, err := h.run(t); err == nil {
		t.Fatal("an unrecoverable tree was reported fine")
	}
	if h.jrnl.intent == nil {
		t.Fatal("the intent was dropped for a node that is not settled")
	}
}

func TestAgent_anUnreadableTreeAfterRecoveryIsAnErrorNotAStaleIntent(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.current = ""
	h.jrnl.intent = &Intent{Version: testVersion, Previous: "0.3.0", StartedAt: time.Now()}
	if _, err := h.run(t); err == nil || h.jrnl.intent == nil {
		t.Fatalf("err %v, intent %+v", err, h.jrnl.intent)
	}
}

// A run killed in the middle of a rollback has put the previous release back
// (so the node no longer runs the intent's) and not yet brought it up. The next
// run finishes the rollback and marks the release bad.
func TestAgent_aRollbackKilledHalfWayIsFinishedByTheNext(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.current, h.node.previous = "0.3.0", "0.3.0"
	h.jrnl.intent = &Intent{Version: testVersion, Previous: "0.3.0", StartedAt: time.Now(), RollingBack: true, Blame: true}
	out, err := h.run(t)
	if err == nil || out.Action != OutcomeFailed {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if want := []string{"recover", "upgrade", "healthy"}; !slices.Equal(h.node.calls, want) {
		t.Fatalf("the node was asked %v, want %v", h.node.calls, want)
	}
	if got := installState(t, db, testVersion, "n2"); got != StateFailed {
		t.Fatalf("n2 recorded %q", got)
	}
	if h.jrnl.intent != nil {
		t.Fatalf("intent %+v survived the finished rollback", h.jrnl.intent)
	}
}
