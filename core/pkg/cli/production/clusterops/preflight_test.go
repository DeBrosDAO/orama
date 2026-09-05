package clusterops

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// A node holds a voter in the platform cluster and in every namespace it was
// allocated to, and those are separate raft groups. Removal used to be checked
// against the platform cluster alone, which is how NODE_REPLACEMENT.md's
// postmortem records a namespace losing quorum.

func voter(id string, reachable bool) rqlite.RaftMember {
	return rqlite.RaftMember{ID: id, Addr: id + ":7001", Voter: true, Reachable: reachable}
}

func TestImpactFor_three_reachable_voters_survive_losing_one(t *testing.T) {
	members := []rqlite.RaftMember{voter("a", true), voter("b", true), voter("c", true)}
	imp := impactFor(PlatformCluster, members, "a")

	if imp.Refusal != "" {
		t.Fatalf("refused a safe removal: %s", imp.Refusal)
	}
	if imp.VotersBefore != 3 || imp.VotersAfter != 2 || imp.QuorumAfter != 2 || imp.ReachableAfter != 2 {
		t.Errorf("counts = before %d, after %d, quorum %d, reachable %d; want 3, 2, 2, 2",
			imp.VotersBefore, imp.VotersAfter, imp.QuorumAfter, imp.ReachableAfter)
	}
}

// Two of three already down: taking the third leaves nobody to elect a leader.
func TestImpactFor_refuses_when_the_survivors_cannot_reach_quorum(t *testing.T) {
	members := []rqlite.RaftMember{voter("a", true), voter("b", false), voter("c", false)}
	imp := impactFor(PlatformCluster, members, "a")

	if imp.Refusal == "" {
		t.Fatal("removing the only reachable voter of three must be refused")
	}
	if imp.ReachableAfter != 0 || imp.QuorumAfter != 2 {
		t.Errorf("reachable %d, quorum %d; want 0, 2", imp.ReachableAfter, imp.QuorumAfter)
	}
}

func TestImpactFor_refuses_removing_the_last_voter(t *testing.T) {
	imp := impactFor(PlatformCluster, []rqlite.RaftMember{voter("a", true)}, "a")
	if imp.Refusal == "" {
		t.Fatal("removing the last voter must be refused")
	}
}

func TestImpactFor_refuses_a_node_that_is_not_a_member(t *testing.T) {
	members := []rqlite.RaftMember{voter("a", true), voter("b", true)}
	if impactFor(PlatformCluster, members, "zzz").Refusal == "" {
		t.Fatal("a node absent from the configuration must be refused, not silently accepted")
	}
}

func TestSafe_is_false_when_any_cluster_refuses(t *testing.T) {
	impacts := []Impact{{Cluster: PlatformCluster}, {Cluster: "anchat", Refusal: "would lose quorum"}}
	if Safe(impacts) {
		t.Fatal("Safe() must be false when one namespace refuses, even if the platform is fine")
	}
	if !Safe([]Impact{{Cluster: PlatformCluster}, {Cluster: "anchat"}}) {
		t.Fatal("Safe() must be true when nothing refuses")
	}
}

func TestFormatImpacts_names_the_cluster_and_the_refusal(t *testing.T) {
	out := FormatImpacts([]Impact{
		{Cluster: PlatformCluster, VotersBefore: 3, VotersAfter: 2, QuorumAfter: 2, ReachableAfter: 2},
		{Cluster: "anchat", VotersBefore: 3, VotersAfter: 2, QuorumAfter: 2, ReachableAfter: 1,
			Refusal: "after removal 1 of 2 voters would be reachable"},
	})
	for _, want := range []string{PlatformCluster, "anchat", "1 of 2 voters"} {
		if !strings.Contains(out, want) {
			t.Errorf("output must mention %q:\n%s", want, out)
		}
	}
}

// --- namespace voters ---

func TestParseNamespaceVoters_groups_by_namespace_and_reads_liveness(t *testing.T) {
	body := []byte(`{"results":[{"columns":["namespace_name","node_id","status"],"values":[
		["anchat","peerA","active"],
		["anchat","peerB","active"],
		["anchat","peerC","inactive"],
		["other","peerA","active"]
	]}]}`)

	got, err := parseNamespaceVoters(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got["anchat"]) != 3 {
		t.Fatalf("anchat has %d voters, want 3", len(got["anchat"]))
	}
	if len(got["other"]) != 1 {
		t.Fatalf("other has %d voters, want 1", len(got["other"]))
	}
	for _, m := range got["anchat"] {
		if !m.Voter {
			t.Errorf("%s must count as a voter", m.ID)
		}
		if m.ID == "peerC" && m.Reachable {
			t.Error("a node whose dns_nodes status is not active must not count as reachable")
		}
		if m.ID == "peerA" && !m.Reachable {
			t.Error("an active node must count as reachable")
		}
	}
}

func TestParseNamespaceVoters_no_namespaces(t *testing.T) {
	got, err := parseNamespaceVoters([]byte(`{"results":[{"columns":["a","b","c"]}]}`))
	if err != nil {
		t.Fatalf("an empty result set is not an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d namespaces, want 0", len(got))
	}
}

func TestParseNamespaceVoters_surfaces_an_rqlite_error(t *testing.T) {
	if _, err := parseNamespaceVoters([]byte(`{"error":"no such table: namespace_clusters"}`)); err == nil {
		t.Fatal("an rqlite error in the body must not be read as an empty cluster list")
	}
}

func TestParseNamespaceVoters_rejects_a_short_row(t *testing.T) {
	body := []byte(`{"results":[{"values":[["anchat","peerA"]]}]}`)
	if _, err := parseNamespaceVoters(body); err == nil {
		t.Fatal("a row missing the status column must be an error, not a node that reads as unreachable")
	}
}

func TestHasMember(t *testing.T) {
	members := []rqlite.RaftMember{voter("a", true), voter("b", true)}
	if !hasMember(members, "b") {
		t.Error("b is a member")
	}
	if hasMember(members, "c") {
		t.Error("c is not a member")
	}
	if hasMember(nil, "a") {
		t.Error("nothing is a member of an empty cluster")
	}
}
