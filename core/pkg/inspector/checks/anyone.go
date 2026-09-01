package checks

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("anyone", CheckAnyone)
}

const anyoneSub = "anyone"

// CheckAnyone runs Anyone client health checks. Relay operator mode is not
// supported; an active leftover orama-anyone-relay unit is a warning.
func CheckAnyone(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	for _, nd := range data.Nodes {
		if nd.Anyone == nil {
			continue
		}
		results = append(results, checkAnyonePerNode(nd)...)
	}

	return results
}

func checkAnyonePerNode(nd *inspector.NodeData) []inspector.CheckResult {
	var r []inspector.CheckResult
	a := nd.Anyone
	node := nd.Node.Name()

	if a.RelayActive {
		r = append(r, inspector.Warn("anyone.relay_leftover", "Anyone relay unit removed", anyoneSub, node,
			"orama-anyone-relay is still active; disable it and run orama-anyone-client", inspector.High))
	}

	if !a.ClientActive && !a.RelayActive {
		return r
	}

	if !a.ClientActive {
		r = append(r, inspector.Fail("anyone.client_active", "Anyone client service active", anyoneSub, node,
			"orama-namespace-anyone-client@index is not active (/v1/proxy/anon needs SOCKS on :9050)", inspector.High))
		return r
	}

	if a.SocksListening {
		r = append(r, inspector.Pass("anyone.socks_listening", "SOCKS5 port 9050 listening", anyoneSub, node,
			"port 9050 bound", inspector.High))
	} else {
		r = append(r, inspector.Fail("anyone.socks_listening", "SOCKS5 port 9050 listening", anyoneSub, node,
			"port 9050 NOT bound (traffic cannot route through anonymity network)", inspector.High))
	}

	if a.ControlListening {
		r = append(r, inspector.Pass("anyone.control_listening", "Control port 9051 listening", anyoneSub, node,
			"port 9051 bound", inspector.Low))
	} else {
		r = append(r, inspector.Warn("anyone.control_listening", "Control port 9051 listening", anyoneSub, node,
			"port 9051 NOT bound (monitoring unavailable)", inspector.Low))
	}

	if a.Bootstrapped {
		r = append(r, inspector.Pass("anyone.client_bootstrapped", "Client bootstrapped", anyoneSub, node,
			fmt.Sprintf("bootstrap=%d%%", a.BootstrapPct), inspector.High))
	} else if a.BootstrapPct > 0 {
		r = append(r, inspector.Warn("anyone.client_bootstrapped", "Client bootstrapped", anyoneSub, node,
			fmt.Sprintf("bootstrap=%d%% (still connecting)", a.BootstrapPct), inspector.High))
	} else {
		r = append(r, inspector.Fail("anyone.client_bootstrapped", "Client bootstrapped", anyoneSub, node,
			"bootstrap=0% (not started or log missing)", inspector.High))
	}

	return r
}
