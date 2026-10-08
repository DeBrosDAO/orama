package autoupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/updatenotice"
	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
)

// auto turns the cluster's policy to auto.
func auto(t *testing.T, h *harness) {
	t.Helper()
	setSetting(t, h.db, updatepolicy.KeyMode, updatepolicy.ModeAuto)
}

func noticeOf(t *testing.T, h *harness) *updatenotice.Notice {
	t.Helper()
	n, err := updatenotice.Read(h.notice)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAgent_aGoodUpdateInstallsOnTheNodeWhoseTurnItIsAndRecordsIt(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2") // a follower: first in the plan
	auto(t, h)

	out, err := h.run(t)
	if err != nil || out.Action != OutcomeInstalled || out.Version != testVersion {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if want := []string{"stage", "upgrade", "healthy"}; !slices.Equal(h.node.calls, want) {
		t.Fatalf("the node was asked %v, want %v", h.node.calls, want)
	}
	if got := installState(t, db, testVersion, "n2"); got != StateInstalled {
		t.Fatalf("n2 recorded %q", got)
	}
	if noticeOf(t, h) != nil {
		t.Fatal("an installed release left a notice")
	}
}

func TestAgent_theRolloutGoesFollowersFirstAndTheLeaderLast(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	order := []string{"10.0.0.2", "10.0.0.3", "10.0.0.1"}
	for i, host := range order {
		h := newHarness(t, db, rel, host)
		auto(t, h)
		// A node whose turn it is not waits, whichever of them wakes first.
		for _, later := range order[i+1:] {
			w := newHarness(t, db, rel, later)
			out, err := w.run(t)
			if err != nil || out.Action != OutcomeWait || len(w.node.calls) != 0 {
				t.Fatalf("%s before its turn: %+v, %v, %v", later, out, err, w.node.calls)
			}
		}
		out, err := h.run(t)
		if err != nil || out.Action != OutcomeInstalled {
			t.Fatalf("%s: %+v, %v", host, out, err)
		}
	}
}

func TestAgent_aReleaseThatDoesNotVerifyIsRefusedAndNothingIsStaged(t *testing.T) {
	t.Run("an archive that is not the target", func(t *testing.T) {
		db, rel := newClusterDB(t), newRelease(t)
		rel.publish(t, 6, time.Time{}, testVersion, []byte("a different archive entirely"))
		h := newHarness(t, db, rel, "10.0.0.2")
		auto(t, h)
		out, err := h.run(t)
		if err != nil || out.Action != ActionRefuse || !strings.Contains(out.Reason, "did not verify") {
			t.Fatalf("outcome %+v, err %v", out, err)
		}
		if len(h.node.calls) != 0 {
			t.Fatalf("a refused archive reached the node: %v", h.node.calls)
		}
		if n := noticeOf(t, h); n == nil || n.State != updatenotice.StateRefused {
			t.Fatalf("notice %+v", n)
		}
	})
	t.Run("a root that did not sign the metadata", func(t *testing.T) {
		db, rel := newClusterDB(t), newRelease(t)
		other := newRelease(t)
		rel.rootOut = other.rootOut
		h := newHarness(t, db, rel, "10.0.0.2")
		auto(t, h)
		out, err := h.run(t)
		if err != nil || out.Action != ActionRefuse || !strings.Contains(out.Reason, "below threshold") {
			t.Fatalf("outcome %+v, err %v", out, err)
		}
		if len(h.node.calls) != 0 {
			t.Fatalf("it reached the node: %v", h.node.calls)
		}
	})
	t.Run("a frozen timestamp", func(t *testing.T) {
		db, rel := newClusterDB(t), newRelease(t)
		rel.publish(t, 6, time.Now().Add(-time.Hour), testVersion, rel.archive)
		h := newHarness(t, db, rel, "10.0.0.2")
		auto(t, h)
		out, err := h.run(t)
		if err != nil || out.Action != ActionRefuse || !strings.Contains(out.Reason, "frozen") {
			t.Fatalf("outcome %+v, err %v", out, err)
		}
		if len(h.node.calls) != 0 {
			t.Fatalf("it reached the node: %v", h.node.calls)
		}
	})
	t.Run("a rolled-back snapshot", func(t *testing.T) {
		db, rel := newClusterDB(t), newRelease(t)
		h := newHarness(t, db, rel, "10.0.0.2")
		auto(t, h)
		if _, err := h.run(t); err != nil { // accepted at snapshot 5
			t.Fatal(err)
		}
		rel.publish(t, 4, time.Time{}, "0.3.2", rel.archive)
		h2 := newHarness(t, db, rel, "10.0.0.3")
		h2.agent.Source.SeenPath = h.agent.Source.SeenPath
		auto(t, h2)
		out, err := h2.run(t)
		if err != nil || out.Action != ActionRefuse || !strings.Contains(out.Reason, "rolled-back") {
			t.Fatalf("outcome %+v, err %v", out, err)
		}
	})
}

func TestAgent_notifyReportsAndInstallsNothing(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2") // mode is not set: notify is the default
	out, err := h.run(t)
	if err != nil || out.Action != ActionNotify || out.Version != testVersion {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if len(h.node.calls) != 0 {
		t.Fatalf("notify touched the node: %v", h.node.calls)
	}
	n := noticeOf(t, h)
	if n == nil || n.State != updatenotice.StateAvailable || n.Candidate != testVersion || n.Current != "0.3.0" || n.Mode != "notify" {
		t.Fatalf("notice %+v", n)
	}
	var locks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cluster_locks WHERE holder <> ''`).Scan(&locks); err != nil || locks != 0 {
		t.Fatalf("notify took the rollout lock (%d), %v", locks, err)
	}
}

func TestAgent_aValidatorOnAutoRefusesToRunAndOnNotifyOnlyReports(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	h.agent.Role = RoleValidator
	auto(t, h)
	if _, err := h.run(t); err == nil || !strings.Contains(err.Error(), "validator") {
		t.Fatalf("a validator on auto: err = %v", err)
	}
	if len(h.node.calls) != 0 {
		t.Fatalf("it touched the node: %v", h.node.calls)
	}
	setSetting(t, db, updatepolicy.KeyMode, updatepolicy.ModeNotify)
	if out, err := h.run(t); err != nil || out.Action != ActionNotify {
		t.Fatalf("a validator on notify: %+v, %v", out, err)
	}
}

func TestAgent_nothingToLookFor(t *testing.T) {
	for name, tc := range map[string]struct {
		prepare func(*testing.T, *harness)
		reason  string
	}{
		"off": {func(t *testing.T, h *harness) { setSetting(t, h.db, updatepolicy.KeyMode, updatepolicy.ModeOff) }, "off"},
		"no repository": {func(t *testing.T, h *harness) {
			setSetting(t, h.db, updatepolicy.KeyRepo, "")
			auto(t, h)
		}, "no release repository"},
		"no adopted root": {func(t *testing.T, h *harness) {
			h.agent.Source.RootPath = h.agent.Source.RootPath + ".missing"
			auto(t, h)
		}, "no release root"},
	} {
		t.Run(name, func(t *testing.T) {
			db, rel := newClusterDB(t), newRelease(t)
			h := newHarness(t, db, rel, "10.0.0.2")
			if err := updatenotice.Write(h.notice, updatenotice.Notice{State: updatenotice.StateAvailable}); err != nil {
				t.Fatal(err)
			}
			tc.prepare(t, h)
			out, err := h.run(t)
			if err != nil || out.Action != ActionNone || !strings.Contains(out.Reason, tc.reason) {
				t.Fatalf("outcome %+v, err %v", out, err)
			}
			if len(h.node.calls) != 0 || noticeOf(t, h) != nil {
				t.Fatalf("calls %v, notice %+v", h.node.calls, noticeOf(t, h))
			}
		})
	}
}

func TestAgent_aDegradedClusterStartsNoUpgrade(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, *harness){
		"a node that stopped heartbeating": func(t *testing.T, h *harness) {
			if _, err := h.db.Exec(`UPDATE dns_nodes SET last_seen = datetime('now', '-1 hour') WHERE id = 'n3'`); err != nil {
				t.Fatal(err)
			}
		},
		"a draining node": func(t *testing.T, h *harness) {
			if _, err := h.db.Exec(`UPDATE dns_nodes SET status = 'draining' WHERE id = 'n3'`); err != nil {
				t.Fatal(err)
			}
		},
		"below a quorum of voters": func(t *testing.T, h *harness) {
			h.agent.Raft = fakeRaft{view: RaftView{LeaderHost: "10.0.0.1", Voters: 3, HealthyVoters: 1}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			db, rel := newClusterDB(t), newRelease(t)
			h := newHarness(t, db, rel, "10.0.0.2")
			auto(t, h)
			mutate(t, h)
			out, err := h.run(t)
			if err != nil || out.Action != ActionRefuse || !strings.Contains(out.Reason, "degraded") {
				t.Fatalf("outcome %+v, err %v", out, err)
			}
			if len(h.node.calls) != 0 {
				t.Fatalf("a degraded cluster was upgraded: %v", h.node.calls)
			}
		})
	}
}

func TestAgent_aRetiredNodeDoesNotCountAsDegraded(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	if _, err := db.Exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status, role, last_seen)
	                      VALUES ('gone', '198.51.100.9', '10.0.0.9', 'offline', 'node', '1970-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	if out, err := h.run(t); err != nil || out.Action != OutcomeInstalled {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
}

func TestAgent_aDowngradeIsRefused(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	h.node.current = "0.4.0"
	auto(t, h)
	out, err := h.run(t)
	if err != nil || out.Action != ActionRefuse || !strings.Contains(out.Reason, "older") {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if len(h.node.calls) != 0 {
		t.Fatalf("a downgrade reached the node: %v", h.node.calls)
	}
}

func TestAgent_outsideTheMaintenanceWindowItOnlyNotifies(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	hour := time.Now().UTC().Hour()
	closed := fmt.Sprintf("%d-%d", (hour+2)%24, (hour+3)%24)
	setSetting(t, db, updatepolicy.KeyWindow, closed)
	out, err := h.run(t)
	if err != nil || out.Action != ActionNotify || !strings.Contains(out.Reason, "outside the maintenance window") {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if len(h.node.calls) != 0 {
		t.Fatalf("it installed outside the window: %v", h.node.calls)
	}
}

func TestAgent_aNodeThatFindsTheLockHeldWaits(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	other := SQLStore{DB: db}
	free, err := other.Lock(t.Context(), "n3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = free(context.Background()) }()
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	out, err := h.run(t)
	if err != nil || out.Action != OutcomeWait || !strings.Contains(out.Reason, "lock") {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if len(h.node.calls) != 0 {
		t.Fatalf("it installed without the lock: %v", h.node.calls)
	}
}

func TestAgent_aCrashedHoldersLeaseExpires(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	if _, err := (SQLStore{DB: db}).Lock(t.Context(), "n3"); err != nil {
		t.Fatal(err)
	}
	// The holder died: its lease ran out.
	if _, err := db.Exec(`UPDATE cluster_locks SET expires_at = datetime('now', '-1 minute') WHERE name = ?`, LockName); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, db, rel, "10.0.0.2")
	auto(t, h)
	if out, err := h.run(t); err != nil || out.Action != OutcomeInstalled {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	var held int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cluster_locks WHERE holder <> ''`).Scan(&held); err != nil || held != 0 {
		t.Fatalf("the lock was not freed after the install: %d, %v", held, err)
	}
}

func TestAgent_aFailedHealthGateRollsBackMarksTheReleaseBadAndStopsTheRollout(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	first := newHarness(t, db, rel, "10.0.0.2")
	first.node.badRelease = true
	auto(t, first)

	out, err := first.run(t)
	if err == nil || out.Action != OutcomeFailed {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	want := []string{"stage", "upgrade", "healthy", "restore", "upgrade", "healthy"}
	if !slices.Equal(first.node.calls, want) {
		t.Fatalf("the node was asked %v, want %v", first.node.calls, want)
	}
	if got := installState(t, db, testVersion, "n2"); got != StateFailed {
		t.Fatalf("n2 recorded %q, want failed", got)
	}
	if n := noticeOf(t, first); n == nil || n.State != updatenotice.StateFailed || n.Current != "0.3.0" {
		t.Fatalf("notice %+v", n)
	}

	// Every other node now refuses the release, so the rollout is over.
	second := newHarness(t, db, rel, "10.0.0.3")
	auto(t, second)
	out, err = second.run(t)
	if err != nil || out.Action != ActionRefuse || !strings.Contains(out.Reason, "marked bad") {
		t.Fatalf("the next node: %+v, %v", out, err)
	}
	if len(second.node.calls) != 0 {
		t.Fatalf("a release marked bad reached another node: %v", second.node.calls)
	}
}

func TestAgent_aFailureBeforeAnythingStoppedDoesNotBlameTheRelease(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	h.node.upgradeErr = fmt.Errorf("prerequisites failed: %w", ErrNotStarted)
	auto(t, h)
	out, err := h.run(t)
	if err == nil || out.Action == OutcomeFailed {
		t.Fatalf("outcome %+v, err %v", out, err)
	}
	if got := installState(t, db, testVersion, "n2"); got != "" {
		t.Fatalf("a failure that stopped nothing was recorded as %q", got)
	}
	if want := []string{"stage", "upgrade", "restore", "healthy"}; !slices.Equal(h.node.calls, want) {
		t.Fatalf("the node was asked %v, want %v", h.node.calls, want)
	}
}

func TestAgent_aStageFailureLeavesTheNodeAlone(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	h.node.stageErr = fmt.Errorf("the release root refused the archive")
	auto(t, h)
	_, err := h.run(t)
	if err == nil || !errors.Is(err, h.node.stageErr) {
		t.Fatalf("err = %v", err)
	}
	if want := []string{"stage"}; !slices.Equal(h.node.calls, want) {
		t.Fatalf("the node was asked %v", h.node.calls)
	}
	if got := installState(t, db, testVersion, "n2"); got != "" {
		t.Fatalf("recorded %q", got)
	}
}

func TestAgent_aStoredSettingItCannotUseStopsTheRun(t *testing.T) {
	db, rel := newClusterDB(t), newRelease(t)
	h := newHarness(t, db, rel, "10.0.0.2")
	setSetting(t, db, updatepolicy.KeyMode, "sometimes")
	if _, err := h.run(t); err == nil {
		t.Fatal("a mode the agent cannot read was treated as the default")
	}
	if _, err := os.Stat(h.notice); err == nil {
		t.Fatal("a notice was written")
	}
}
