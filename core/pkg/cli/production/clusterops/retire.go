package clusterops

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// RetireStep is one statement of the retirement, with what it is for.
type RetireStep struct {
	// What the step accomplishes, printed to the operator.
	What string
	// SQL is the statement, shown by --dry-run and executed otherwise.
	SQL string
}

// RetirementPlan is every store a departing node has to be taken out of.
//
// The node keeps a row in five tables beyond raft and no reconciler removes
// four of them: namespace_cluster_nodes and namespace_port_allocations keep the
// node listed as a member of every namespace it served, webrtc_port_allocations
// keeps its TURN and SFU allocations so the namespace never re-places those
// services, and dns_nameservers keeps its NS slot. The stale-node purge that
// runs in the cluster only ever logged that these existed.
//
// dns_nodes is UPDATEd, not DELETEd. Every DNS cleanup the cluster performs —
// the node's own A records, its NS glue, and the namespace TURN and host
// records pointing at it — finds the IP by looking for a dns_nodes row that is
// not active. Deleting the row removed the cluster's only handle on those
// records and stranded them permanently. Marking it retires the node and lets
// the purge run with its own guards, which know when deleting the last A record
// for a name would silently misroute traffic rather than fail it.
func RetirementPlan(rec NodeRecord) []RetireStep {
	peer := SQLLiteral(rec.PeerID)
	return []RetireStep{
		{
			What: "mark the node retired so the cluster purges its DNS records",
			SQL: fmt.Sprintf(
				`UPDATE dns_nodes SET status = 'inactive', last_seen = '%s', updated_at = datetime('now') WHERE id = '%s'`,
				retiredLastSeen, peer),
		},
		{
			What: "release the mesh address",
			SQL:  fmt.Sprintf(`DELETE FROM wireguard_peers WHERE node_id = '%s'`, peer),
		},
		{
			What: "release the nameserver slot",
			SQL:  fmt.Sprintf(`DELETE FROM dns_nameservers WHERE node_id = '%s'`, peer),
		},
		{
			What: "remove it from every namespace cluster",
			SQL:  fmt.Sprintf(`DELETE FROM namespace_cluster_nodes WHERE node_id = '%s'`, peer),
		},
		{
			What: "free its namespace port blocks",
			SQL:  fmt.Sprintf(`DELETE FROM namespace_port_allocations WHERE node_id = '%s'`, peer),
		},
		{
			What: "free its TURN and SFU allocations so those roles get re-placed",
			SQL:  fmt.Sprintf(`DELETE FROM webrtc_port_allocations WHERE node_id = '%s'`, peer),
		},
		{
			// Revoked, not deleted. A row with revoked_at set verifies nothing
			// AND cannot be enrolled again, so the retired machine's disk stops
			// being a credential and the machine cannot re-admit itself through
			// the first-use path. Deleting the row would restore exactly that.
			What: "revoke its key so the machine can no longer speak as this node",
			SQL: fmt.Sprintf(
				`UPDATE node_credentials SET revoked_at = datetime('now') WHERE node_id = '%s' AND revoked_at IS NULL`,
				peer),
		},
	}
}

// retiredLastSeen is the last_seen a retired node is given.
//
// The cluster's DNS purge only touches a node that has been silent longer than
// its stale window, so that a rolling restart or a leaderless minute never
// deletes records. An operator removal is not a blip and should not wait out
// that window, so the node is backdated past it. The date is fixed rather than
// computed: rqlite replicates the statement text and each node applies it
// locally, so datetime('now', '-1 day') in a write would land differently on
// every replica.
const retiredLastSeen = "1970-01-01 00:00:00"

// Retire takes the node out of every membership store.
//
// Each statement is a DELETE or an idempotent UPDATE keyed on the node, so
// re-running a retirement that failed half way through is safe and finishes it.
func Retire(survivor inspector.Node, rec NodeRecord) error {
	for _, step := range RetirementPlan(rec) {
		if err := ExecSQL(survivor, step.SQL); err != nil {
			return fmt.Errorf("%s: %w", step.What, err)
		}
		fmt.Printf("  ✓ %s\n", step.What)
	}
	return nil
}
