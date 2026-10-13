package autoupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/updatenotice"
	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
)

// failRecord is a Store whose writes of a failed install do not go through, as
// when the registry is unreachable at the moment the release has been put back.
type failRecord struct {
	Store
	err error
}

func (f *failRecord) Record(ctx context.Context, version, nodeID, state, detail string) error {
	if state == StateFailed && f.err != nil {
		return f.err
	}
	return f.Store.Record(ctx, version, nodeID, state, detail)
}

// A release that failed and was rolled back whose failure could not be written
// down must not be forgotten: with the intent cleared, the next tick would
// install the same release again, and so would every tick after it.
func TestAgent_aFailureThatCouldNotBeRecordedIsRecordedByTheNextRunAndNothingIsInstalledAgain(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.badRelease = true
	store := &failRecord{Store: h.agent.Store, err: errors.New("the registry is unreachable")}
	h.agent.Store = store

	_, err := h.run(t)
	if err == nil || !strings.Contains(err.Error(), "the registry is unreachable") {
		t.Fatalf("err = %v", err)
	}
	if h.jrnl.intent == nil || !h.jrnl.intent.RolledBack || h.jrnl.intent.Version != testVersion {
		t.Fatalf("intent %+v: the failure is owed to the registry and the journal forgot it", h.jrnl.intent)
	}
	if got := installState(t, db, testVersion, "n2"); got != "" {
		t.Fatalf("n2 recorded %q", got)
	}
	calls := slices.Clone(h.node.calls)

	// Still unreachable: nothing is fetched or installed either.
	if _, err := h.run(t); err == nil || h.jrnl.intent == nil {
		t.Fatalf("err = %v, intent %+v", err, h.jrnl.intent)
	}

	store.err = nil
	out, err := h.run(t)
	if err == nil || out.Action != OutcomeFailed || out.Version != testVersion {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if got := installState(t, db, testVersion, "n2"); got != StateFailed {
		t.Fatalf("n2 recorded %q, want failed", got)
	}
	if h.jrnl.intent != nil {
		t.Fatalf("the intent survived the recorded failure: %+v", h.jrnl.intent)
	}
	// Only the recoveries ran: no stage, no upgrade, no second rollback.
	if want := append(calls, "recover", "recover"); !slices.Equal(h.node.calls, want) {
		t.Fatalf("the node was asked %v, want %v", h.node.calls, want)
	}
	var detail string
	if err := db.QueryRow(`SELECT detail FROM release_installs WHERE version = ? AND node_id = 'n2'`, testVersion).Scan(&detail); err != nil ||
		!strings.Contains(detail, "did not rejoin") {
		t.Fatalf("the recorded failure lost its reason: %q, %v", detail, err)
	}
	if n := noticeOf(t, h); n == nil || n.State != updatenotice.StateFailed || n.Mode != updatepolicy.ModeAuto || n.Channel != updatepolicy.DefaultChannel {
		t.Fatalf("notice %+v", n)
	}
}

// A release that could not start installing is not fetched again every tick:
// the wait doubles with each failure and is reported.
func TestAgent_aReleaseThatCouldNotStartInstallingIsRetriedWithABackoff(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.upgradeErr = fmt.Errorf("prerequisites failed: %w", ErrNotStarted)
	clock := time.Now()
	h.agent.Now = func() time.Time { return clock }

	if _, err := h.run(t); err == nil {
		t.Fatal("a failed install was reported fine")
	}
	r := h.retry.retry
	if r == nil || r.Version != testVersion || r.Attempts != 1 || !r.Until.Equal(clock.UTC().Add(retryBase)) {
		t.Fatalf("retry %+v", r)
	}
	n := noticeOf(t, h)
	if n == nil || n.State != updatenotice.StateFailed || n.Candidate != testVersion ||
		!strings.Contains(n.Reason, "next try after") || !strings.Contains(n.Reason, "prerequisites failed") {
		t.Fatalf("notice %+v", n)
	}
	calls := len(h.node.calls)

	clock = clock.Add(time.Minute)
	for i := 0; i < 3; i++ {
		out, err := h.run(t)
		if err != nil || out.Action != OutcomeWait || out.Version != testVersion || !strings.Contains(out.Reason, "not tried again before") {
			t.Fatalf("inside the wait: %+v, %v", out, err)
		}
	}
	if len(h.node.calls) != calls {
		t.Fatalf("a tick inside the wait touched the node: %v", h.node.calls[calls:])
	}
	if got := noticeOf(t, h); got == nil || got.State != updatenotice.StateFailed {
		t.Fatalf("the wait lost the notice: %+v", got)
	}

	// The wait over: another try, which fails again and waits twice as long.
	clock = clock.Add(retryBase)
	h.node.restored = false
	if _, err := h.run(t); err == nil {
		t.Fatal("a failed install was reported fine")
	}
	if r := h.retry.retry; r == nil || r.Attempts != 2 || !r.Until.Equal(clock.UTC().Add(2*retryBase)) {
		t.Fatalf("retry %+v", r)
	}

	// Fixed, it installs and the record is gone.
	clock = clock.Add(2 * retryBase)
	h.node.upgradeErr = nil
	if out, err := h.run(t); err != nil || out.Action != OutcomeInstalled {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if h.retry.retry != nil {
		t.Fatalf("an installed release left a retry: %+v", h.retry.retry)
	}
}

func TestAgent_aStageThatChangedNothingBacksOffToo(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.node.stageErr = fmt.Errorf("no space left on device")
	if _, err := h.run(t); err == nil || h.retry.retry == nil {
		t.Fatalf("err = %v, retry %+v", err, h.retry.retry)
	}
	out, err := h.run(t)
	if err != nil || out.Action != OutcomeWait {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if want := []string{"stage"}; !slices.Equal(h.node.calls, want) {
		t.Fatalf("the node was asked %v, want only the first stage", h.node.calls)
	}
}

// A newer release is not held back by the wait the older one earned.
func TestAgent_aNewerReleaseIsNotHeldByTheWaitOfAnOlderOne(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	h.retry.retry = &Retry{Version: "0.3.0-older", Attempts: 4, Until: time.Now().Add(time.Hour)}
	if out, err := h.run(t); err != nil || out.Action != OutcomeInstalled {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
}

// A wait longer than the longest one ever recorded was written by a clock that
// has since gone back: it does not hold the release off, and the longest wait
// there is still holds.
func TestAgent_aWaitFromAClockThatWentBackIsOver(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	clock := time.Now()
	h.agent.Now = func() time.Time { return clock }

	h.retry.retry = &Retry{Version: testVersion, Attempts: 7, Until: clock.Add(retryMax)}
	if out, err := h.run(t); err != nil || out.Action != OutcomeWait {
		t.Fatalf("the longest wait there is did not hold: %+v, %v", out, err)
	}

	h.retry.retry = &Retry{Version: testVersion, Attempts: 7, Until: clock.Add(retryMax + time.Hour)}
	if out, err := h.run(t); err != nil || out.Action != OutcomeInstalled {
		t.Fatalf("a wait from a clock that went back held the release: %+v, %v", out, err)
	}
}

func TestRetryDelay_doublesAndIsCapped(t *testing.T) {
	for attempts, want := range map[int]time.Duration{1: retryBase, 2: 2 * retryBase, 3: 4 * retryBase, 4: 8 * retryBase, 5: 16 * retryBase, 6: 32 * retryBase, 7: retryMax, 40: retryMax} {
		if got := retryDelay(attempts); got != want {
			t.Errorf("retryDelay(%d) = %v, want %v", attempts, got, want)
		}
	}
}

// A run killed while it fetched leaves its directory, with an archive in it,
// for ever; the next run clears it before it fetches its own.
func TestAgent_aFetchDirectoryAKilledRunLeftIsSweptAtTheStartOfTheNext(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	work := h.agent.Source.WorkDir
	stale := filepath.Join(work, "fetch-123456")
	if err := os.MkdirAll(filepath.Join(stale, "metadata"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "release.tar.gz"), []byte("half an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(work, "install-intent.json")
	if err := os.WriteFile(other, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stale fetch directory is still there: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("a file that is not a fetch directory was removed: %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(work, "fetch-*"))
	if len(left) != 0 {
		t.Fatalf("this run's own fetch directory was left: %v", left)
	}
}

// A policy that does not parse stops a run from looking for releases; it must
// not stop one from repairing a node a killed run left half-installed.
func TestAgent_aPolicyThatDoesNotParseDoesNotBlockRecovery(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	h.node.current = testVersion // staged
	h.jrnl.intent = &Intent{Version: testVersion, Previous: "0.3.0", StartedAt: time.Now()}
	setSetting(t, db, updatepolicy.KeyMode, "sometimes")

	out, err := h.run(t)
	if err != nil || out.Action != OutcomeInstalled {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if h.jrnl.intent != nil {
		t.Fatalf("intent %+v survived", h.jrnl.intent)
	}
	// The install is done; the policy is still the operator's problem.
	if _, err := h.run(t); err == nil {
		t.Fatal("a mode the agent cannot read was treated as the default")
	}
}
