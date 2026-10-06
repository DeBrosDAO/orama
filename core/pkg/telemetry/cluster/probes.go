package cluster

import (
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// Every node runs the gateway, the database, the cache, storage, the vault and
// the mesh; a probe for those always applies. DNS and the chain run on some
// nodes only, and apply where the node reported on them.

func probeGateway(r *report.NodeReport) (bool, bool) {
	return true, r.Gateway != nil && r.Gateway.Responsive && r.Gateway.HTTPStatus == http.StatusOK
}

func probeDatabase(r *report.NodeReport) (bool, bool) {
	q := r.RQLite
	return true, q != nil && q.Responsive && q.Settled()
}

func probeCache(r *report.NodeReport) (bool, bool) {
	return true, r.Olric != nil && r.Olric.ServiceActive && r.Olric.MemberlistUp
}

func probeStorage(r *report.NodeReport) (bool, bool) {
	return true, r.IPFS != nil && r.IPFS.DaemonActive && r.IPFS.ClusterActive
}

func probeVault(r *report.NodeReport) (bool, bool) {
	return true, r.Vault != nil && r.Vault.ServiceActive && r.Vault.Responsive
}

func probeDNS(r *report.NodeReport) (bool, bool) {
	if r.DNS == nil {
		return false, false
	}
	return true, r.DNS.CoreDNSActive && r.DNS.CaddyActive
}

// probeMesh counts a node healthy when its interface is up and every peer has
// handshaked recently. A peer that never handshaked is down.
func probeMesh(r *report.NodeReport) (bool, bool) {
	wg := r.WireGuard
	if wg == nil || !wg.InterfaceUp {
		return true, false
	}
	for _, p := range wg.Peers {
		if p.LatestHandshake == 0 || p.HandshakeAgeSec > wgHandshakeStaleSec {
			return true, false
		}
	}
	return true, true
}

// probeChain counts a node healthy when its RPC answers, it is in sync, and
// it has seen a block recently. A halted chain is unhealthy on every node.
func probeChain(r *report.NodeReport) (bool, bool) {
	c := r.Chain
	if c == nil {
		return false, false
	}
	return true, c.Responsive && !c.CatchingUp && c.BlockAgeSec < chainStallSec
}

// databaseOutage is raft's own answer: the database cannot commit a write
// when no node leads, or when the leader sees no majority of voters
// reachable. Non-voters carry reads and never decide quorum, so losing them
// degrades the database but never takes it down.
//
// A leader is known when a reporting node leads, or a reporting follower
// names its leader — the leader's own report may be missing: the rolling
// upgrade does it last, so for most of a rollout it runs an older release,
// and its gateway can be down while raft is fine. With no leader known and
// some node silent or unknown, the snapshot cannot tell an election from a
// leader that simply did not report, so that is not called an outage either:
// the component's node counts still show it degraded.
func databaseOutage(snap *ClusterSnapshot) bool {
	var leader *report.RQLiteReport
	leaderKnown := false
	for _, r := range snap.Healthy() {
		q := r.RQLite
		if q == nil || !q.Responsive {
			continue
		}
		switch {
		case q.RaftState == report.RaftLeader:
			leader, leaderKnown = q, true
		case q.RaftState == report.RaftFollower && (q.LeaderID != "" || q.LeaderAddr != ""):
			leaderKnown = true
		}
	}
	if !leaderKnown {
		return !hasSilentNode(snap)
	}
	return leader != nil && votersBelowMajority(leader)
}

// votersBelowMajority reads the leader's view of the membership.
func votersBelowMajority(leader *report.RQLiteReport) bool {
	voters, reachable := 0, 0
	for _, n := range leader.Nodes {
		if !n.Voter {
			continue
		}
		voters++
		if n.Reachable {
			reachable++
		}
	}
	return voters > 0 && reachable*2 <= voters
}

// hasSilentNode is whether any node sent no usable report.
func hasSilentNode(snap *ClusterSnapshot) bool {
	for _, n := range snap.Nodes {
		if n.Unknown || n.Report == nil {
			return true
		}
	}
	return false
}
