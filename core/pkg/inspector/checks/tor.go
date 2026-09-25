package checks

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("tor", CheckTor)
}

const torSub = "tor"

// CheckTor checks the node's client-only Tor daemon, which every node runs for
// /v1/proxy/anon, /v1/proxy/tunnel and the anon_fetch host function, and flags
// any Anyone network left behind by an incomplete migration.
func CheckTor(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult
	for _, nd := range data.Nodes {
		if nd.Tor == nil {
			continue
		}
		results = append(results, checkTorPerNode(nd)...)
	}
	return results
}

func checkTorPerNode(nd *inspector.NodeData) []inspector.CheckResult {
	var r []inspector.CheckResult
	t := nd.Tor
	node := nd.Node.Name()

	if t.LegacyAnyone {
		r = append(r, inspector.Warn("tor.legacy_anyone", "Anyone network removed", torSub, node,
			"Anyone units or files are still present; run `orama node upgrade` on this node to remove them", inspector.Medium))
	}

	if !t.ClientActive {
		r = append(r, inspector.Fail("tor.client_active", "Tor client service active", torSub, node,
			fmt.Sprintf("%s is not active (/v1/proxy/anon and anon_fetch need SOCKS on %s)", inspector.TorUnit, constants.TorSOCKSAddr()), inspector.High))
		return r
	}
	r = append(r, inspector.Pass("tor.client_active", "Tor client service active", torSub, node,
		inspector.TorUnit+" active", inspector.High))

	socksName := fmt.Sprintf("SOCKS5 port %d listening", constants.TorSOCKSPort)
	if t.SocksListening {
		r = append(r, inspector.Pass("tor.socks_listening", socksName, torSub, node,
			fmt.Sprintf("port %d bound", constants.TorSOCKSPort), inspector.High))
	} else {
		r = append(r, inspector.Fail("tor.socks_listening", socksName, torSub, node,
			fmt.Sprintf("port %d NOT bound (traffic cannot route through Tor)", constants.TorSOCKSPort), inspector.High))
	}

	return append(r, checkTorBootstrap(t, node))
}

func checkTorBootstrap(t *inspector.TorData, node string) inspector.CheckResult {
	const id, name = "tor.client_bootstrapped", "Tor client bootstrapped"
	switch {
	case t.Bootstrapped:
		return inspector.Pass(id, name, torSub, node, fmt.Sprintf("bootstrap=%d%%", t.BootstrapPct), inspector.High)
	case t.BootstrapPct == inspector.TorBootstrapUnknown:
		return inspector.Warn(id, name, torSub, node,
			"no bootstrap line in the running process's journal (vacuumed, or journal unreadable)", inspector.Low)
	case t.BootstrapPct > 0:
		return inspector.Warn(id, name, torSub, node,
			fmt.Sprintf("bootstrap=%d%% (still connecting)", t.BootstrapPct), inspector.High)
	default:
		return inspector.Fail(id, name, torSub, node, "bootstrap=0% (not connected to the Tor network)", inspector.High)
	}
}
