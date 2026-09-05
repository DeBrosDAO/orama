package clusterops

import (
	"strings"
	"testing"
)

// Four of these tables had no reconciler that ever removed a departed node, and
// the fifth — dns_nodes — was DELETEd, which cut the cluster's only handle on
// the node's DNS records and stranded them for good.

func planSQL(t *testing.T, peerID string) string {
	t.Helper()
	var b strings.Builder
	for _, step := range RetirementPlan(NodeRecord{PeerID: peerID, InternalIP: "10.0.0.5"}) {
		if step.What == "" {
			t.Error("every step must say what it is for; it is printed to the operator")
		}
		b.WriteString(step.SQL)
		b.WriteString("\n")
	}
	return b.String()
}

func TestRetirementPlan_covers_every_store_that_keeps_the_node(t *testing.T) {
	sql := planSQL(t, "peerA")
	for _, table := range []string{
		"wireguard_peers",
		"dns_nameservers",
		"namespace_cluster_nodes",
		"namespace_port_allocations",
		"webrtc_port_allocations",
		"dns_nodes",
		"node_credentials",
	} {
		if !strings.Contains(sql, table) {
			t.Errorf("the plan never touches %s; a departed node stays listed there", table)
		}
	}
}

// The purge that deletes a node's A records, its NS glue and the namespace TURN
// and host records pointing at it finds the IP through a dns_nodes row that is
// not active. Deleting the row makes those records unreachable forever.
func TestRetirementPlan_marks_dns_nodes_rather_than_deleting_it(t *testing.T) {
	for _, step := range RetirementPlan(NodeRecord{PeerID: "peerA"}) {
		if !strings.Contains(step.SQL, "dns_nodes") {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(step.SQL)), "DELETE") {
			t.Fatal("dns_nodes must be UPDATEd to inactive, never deleted")
		}
		if !strings.Contains(step.SQL, "'inactive'") {
			t.Error("the node must be marked inactive so the cluster's purge picks it up")
		}
		if !strings.Contains(step.SQL, "last_seen") {
			t.Error("last_seen must be backdated past the stale window, or the purge waits it out")
		}
		return
	}
	t.Fatal("no step touches dns_nodes")
}

// rqlite replicates statement text and each node applies it locally, so a
// non-deterministic value in a write can land differently on every replica.
func TestRetirementPlan_backdates_with_a_fixed_instant(t *testing.T) {
	sql := planSQL(t, "peerA")
	if strings.Contains(sql, "datetime('now', '-") || strings.Contains(sql, "datetime('now','-") {
		t.Error("a relative timestamp in a write diverges the replicas; the backdate must be a constant")
	}
	if !strings.Contains(sql, retiredLastSeen) {
		t.Errorf("the plan must write the fixed %q", retiredLastSeen)
	}
}

func TestRetirementPlan_keys_every_statement_on_the_node(t *testing.T) {
	for _, step := range RetirementPlan(NodeRecord{PeerID: "peerA", InternalIP: "10.0.0.5"}) {
		if !strings.Contains(step.SQL, "WHERE") {
			t.Fatalf("unscoped statement would hit every node: %s", step.SQL)
		}
		if !strings.Contains(step.SQL, "peerA") {
			t.Errorf("statement is not scoped to the target node: %s", step.SQL)
		}
	}
}

// Values reach these statements from an operator-typed IP and from the database.
func TestRetirementPlan_escapes_the_peer_id(t *testing.T) {
	sql := planSQL(t, "pe'er")
	if strings.Contains(sql, "'pe'er'") {
		t.Fatal("a quote in the peer id must be escaped, or it terminates the literal")
	}
	if !strings.Contains(sql, "pe''er") {
		t.Error("the escaped form must be present")
	}
}

// A removal that failed part way through is finished by running it again, so
// nothing may depend on a row still existing.
func TestRetirementPlan_is_idempotent(t *testing.T) {
	for _, step := range RetirementPlan(NodeRecord{PeerID: "peerA"}) {
		verb := strings.ToUpper(strings.Fields(strings.TrimSpace(step.SQL))[0])
		if verb != "DELETE" && verb != "UPDATE" {
			t.Errorf("%s is not repeatable: %s", verb, step.SQL)
		}
	}
}

// A retired node's credential is revoked, not deleted. A row with revoked_at
// set verifies nothing AND cannot be enrolled again; deleting it would send the
// machine back down the never-seen path, where it could enrol a key of its own
// choosing and speak as that node again.
func TestRetirementPlan_revokesTheNodeKeyRatherThanDeletingIt(t *testing.T) {
	sql := planSQL(t, "peerA")

	if strings.Contains(sql, "DELETE FROM node_credentials") {
		t.Error("retirement deletes the node's credential, which lets the machine re-enrol itself")
	}
	if !strings.Contains(sql, "UPDATE node_credentials SET revoked_at") {
		t.Error("retirement does not revoke the node's credential, so its disk stays a working credential")
	}
}
