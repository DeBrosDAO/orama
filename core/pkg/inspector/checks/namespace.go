package checks

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("namespace", CheckNamespace)
}

const nsSub = "namespace"

// CheckNamespace runs all namespace-level health checks.
func CheckNamespace(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	for _, nd := range data.Nodes {
		if len(nd.Namespaces) == 0 {
			continue
		}
		results = append(results, checkNamespacesPerNode(nd)...)
	}

	results = append(results, checkNamespacesCrossNode(data)...)

	return results
}

func checkNamespacesPerNode(nd *inspector.NodeData) []inspector.CheckResult {
	var r []inspector.CheckResult
	node := nd.Node.Name()

	for _, ns := range nd.Namespaces {
		prefix := fmt.Sprintf("ns.%s", ns.Name)

		// RQLite health
		if ns.RQLiteUp {
			r = append(r, inspector.Pass(prefix+".rqlite_up", fmt.Sprintf("Namespace %s RQLite responding", ns.Name), nsSub, node,
				fmt.Sprintf("port_base=%d state=%s", ns.PortBase, ns.RQLiteState), inspector.Critical))
		} else {
			r = append(r, inspector.Fail(prefix+".rqlite_up", fmt.Sprintf("Namespace %s RQLite responding", ns.Name), nsSub, node,
				fmt.Sprintf("port_base=%d not responding", ns.PortBase), inspector.Critical))
		}

		// RQLite Raft state
		if ns.RQLiteUp {
			switch ns.RQLiteState {
			case "Leader", "Follower":
				r = append(r, inspector.Pass(prefix+".rqlite_state", fmt.Sprintf("Namespace %s RQLite raft state valid", ns.Name), nsSub, node,
					fmt.Sprintf("state=%s", ns.RQLiteState), inspector.Critical))
			case "Candidate":
				r = append(r, inspector.Warn(prefix+".rqlite_state", fmt.Sprintf("Namespace %s RQLite raft state valid", ns.Name), nsSub, node,
					"state=Candidate (election in progress)", inspector.Critical))
			default:
				r = append(r, inspector.Fail(prefix+".rqlite_state", fmt.Sprintf("Namespace %s RQLite raft state valid", ns.Name), nsSub, node,
					fmt.Sprintf("state=%s", ns.RQLiteState), inspector.Critical))
			}
		}

		// RQLite readiness
		if ns.RQLiteReady {
			r = append(r, inspector.Pass(prefix+".rqlite_ready", fmt.Sprintf("Namespace %s RQLite ready", ns.Name), nsSub, node,
				"/readyz OK", inspector.Critical))
		} else if ns.RQLiteUp {
			r = append(r, inspector.Fail(prefix+".rqlite_ready", fmt.Sprintf("Namespace %s RQLite ready", ns.Name), nsSub, node,
				"/readyz failed", inspector.Critical))
		}

		// Olric health
		if ns.OlricUp {
			r = append(r, inspector.Pass(prefix+".olric_up", fmt.Sprintf("Namespace %s Olric port listening", ns.Name), nsSub, node,
				"memberlist port bound", inspector.High))
		} else {
			r = append(r, inspector.Fail(prefix+".olric_up", fmt.Sprintf("Namespace %s Olric port listening", ns.Name), nsSub, node,
				"memberlist port not bound", inspector.High))
		}

		// Gateway health
		if ns.GatewayUp {
			r = append(r, inspector.Pass(prefix+".gateway_up", fmt.Sprintf("Namespace %s Gateway responding", ns.Name), nsSub, node,
				fmt.Sprintf("HTTP status=%d", ns.GatewayStatus), inspector.High))
		} else {
			r = append(r, inspector.Fail(prefix+".gateway_up", fmt.Sprintf("Namespace %s Gateway responding", ns.Name), nsSub, node,
				fmt.Sprintf("HTTP status=%d", ns.GatewayStatus), inspector.High))
		}
	}

	return r
}

func checkNamespacesCrossNode(data *inspector.ClusterData) []inspector.CheckResult {
	var r []inspector.CheckResult

	// Collect all namespace names across nodes
	nsNodes := map[string]int{}   // namespace name → count of nodes running it
	nsHealthy := map[string]int{} // namespace name → count of nodes where all services are up

	for _, nd := range data.Nodes {
		for _, ns := range nd.Namespaces {
			nsNodes[ns.Name]++
			if ns.RQLiteUp && ns.OlricUp && ns.GatewayUp {
				nsHealthy[ns.Name]++
			}
		}
	}

	for name, total := range nsNodes {
		healthy := nsHealthy[name]
		if healthy == total {
			r = append(r, inspector.Pass(
				fmt.Sprintf("ns.%s.all_healthy", name),
				fmt.Sprintf("Namespace %s healthy on all nodes", name),
				nsSub, "",
				fmt.Sprintf("%d/%d nodes fully healthy", healthy, total),
				inspector.Critical))
		} else {
			r = append(r, inspector.Fail(
				fmt.Sprintf("ns.%s.all_healthy", name),
				fmt.Sprintf("Namespace %s healthy on all nodes", name),
				nsSub, "",
				fmt.Sprintf("%d/%d nodes fully healthy", healthy, total),
				inspector.Critical))
		}

		// Check namespace has quorum (>= N/2+1 RQLite instances)
		rqliteUp := 0
		for _, nd := range data.Nodes {
			for _, ns := range nd.Namespaces {
				if ns.Name == name && ns.RQLiteUp {
					rqliteUp++
				}
			}
		}
		quorumNeeded := total/2 + 1
		if rqliteUp >= quorumNeeded {
			r = append(r, inspector.Pass(
				fmt.Sprintf("ns.%s.quorum", name),
				fmt.Sprintf("Namespace %s RQLite quorum", name),
				nsSub, "",
				fmt.Sprintf("rqlite_up=%d/%d quorum_needed=%d", rqliteUp, total, quorumNeeded),
				inspector.Critical))
		} else {
			r = append(r, inspector.Fail(
				fmt.Sprintf("ns.%s.quorum", name),
				fmt.Sprintf("Namespace %s RQLite quorum", name),
				nsSub, "",
				fmt.Sprintf("rqlite_up=%d/%d quorum_needed=%d (QUORUM LOST)", rqliteUp, total, quorumNeeded),
				inspector.Critical))
		}
	}

	return r
}
