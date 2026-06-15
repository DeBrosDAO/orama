package namespace

import (
	"testing"
	"time"
)

// Bugboard #708 — the leadership-locality reconciler hands leadership off a
// geographically-isolated namespace raft leader to the nearest co-located
// voter, without changing membership. These pin the decision logic.

const thr = 100 * time.Millisecond

func TestDecideLeadershipTransfer_isolatedLeaderTransfersToNearest(t *testing.T) {
	// Distant leader (109): both peers are far. Transfer to the NEAREST (57 @235ms).
	peers := map[string]time.Duration{
		"10.0.0.6:10001": 256 * time.Millisecond, // 51
		"10.0.0.1:10001": 235 * time.Millisecond, // 57
	}
	target, transfer := decideLeadershipTransfer(true, true, true, peers, thr)
	if !transfer {
		t.Fatal("an isolated leader (closest peer 235ms > 100ms) must transfer")
	}
	if target != "10.0.0.1:10001" {
		t.Errorf("must transfer to the NEAREST peer; got %q", target)
	}
}

func TestDecideLeadershipTransfer_centralLeaderStays(t *testing.T) {
	// Co-located leader (51): has a nearby peer (57 @20ms) and a distant one (109).
	// min RTT 20ms < 100ms → leader is central → NO transfer (the correct steady state).
	peers := map[string]time.Duration{
		"10.0.0.1:10001":  20 * time.Millisecond,  // 57 (close)
		"10.0.0.11:10001": 256 * time.Millisecond, // 109 (far)
	}
	if _, transfer := decideLeadershipTransfer(true, true, true, peers, thr); transfer {
		t.Error("a leader with a nearby voter is central enough; must NOT transfer")
	}
}

func TestDecideLeadershipTransfer_allDistantTransfersToNearest(t *testing.T) {
	// Pathological all-mutually-distant topology: every peer is far, so there is
	// no truly co-located target. The reconciler still moves to the NEAREST
	// (best available); the per-namespace cooldown (TestLeaderTransferCooldown)
	// is what bounds the resulting churn to ~one transfer per node per window.
	peers := map[string]time.Duration{
		"a": 250 * time.Millisecond,
		"b": 210 * time.Millisecond,
	}
	target, transfer := decideLeadershipTransfer(true, true, true, peers, thr)
	if !transfer || target != "b" {
		t.Errorf("all-distant: expected transfer to nearest 'b'; got transfer=%v target=%q", transfer, target)
	}
}

func TestDecideLeadershipTransfer_guards(t *testing.T) {
	farPeers := map[string]time.Duration{"p": 300 * time.Millisecond}

	if _, transfer := decideLeadershipTransfer(false, true, true, farPeers, thr); transfer {
		t.Error("non-leader must never transfer")
	}
	if _, transfer := decideLeadershipTransfer(true, false, true, farPeers, thr); transfer {
		t.Error("must not transfer when a voter is unreachable (degraded cluster)")
	}
	if _, transfer := decideLeadershipTransfer(true, true, false, farPeers, thr); transfer {
		t.Error("must not transfer during cooldown")
	}
	if _, transfer := decideLeadershipTransfer(true, true, true, map[string]time.Duration{}, thr); transfer {
		t.Error("must not transfer with no measurable peers (single-node / all-unreachable)")
	}
}

func TestDecideLeadershipTransfer_exactlyThresholdStays(t *testing.T) {
	// Closest peer exactly at the threshold is NOT > threshold → stay (no churn at the boundary).
	peers := map[string]time.Duration{"p": thr}
	if _, transfer := decideLeadershipTransfer(true, true, true, peers, thr); transfer {
		t.Error("RTT exactly at the threshold must not trigger a transfer")
	}
}

func TestLeaderTransferCooldown(t *testing.T) {
	cm := &ClusterManager{}
	if !cm.leaderTransferCooldownElapsed("ns") {
		t.Error("fresh namespace (no prior transfer) must be out of cooldown")
	}
	cm.recordLeaderTransfer("ns")
	if cm.leaderTransferCooldownElapsed("ns") {
		t.Error("immediately after a transfer the namespace must be in cooldown")
	}
	if !cm.leaderTransferCooldownElapsed("other-ns") {
		t.Error("cooldown must be per-namespace")
	}
}
