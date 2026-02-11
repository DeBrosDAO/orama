package checks

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("anyone", CheckAnyone)
}

const anyoneSub = "anyone"

// CheckAnyone runs all Anyone relay/client health checks.
func CheckAnyone(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	for _, nd := range data.Nodes {
		if nd.Anyone == nil {
			continue
		}
		results = append(results, checkAnyonePerNode(nd)...)
	}

	results = append(results, checkAnyoneCrossNode(data)...)

	return results
}

func checkAnyonePerNode(nd *inspector.NodeData) []inspector.CheckResult {
	var r []inspector.CheckResult
	a := nd.Anyone
	node := nd.Node.Name()

	// If neither service is active, skip all checks for this node
	if !a.RelayActive && !a.ClientActive {
		return r
	}

	isClientMode := a.Mode == "client"

	if a.RelayActive {
		r = append(r, inspector.Pass("anyone.relay_active", "Anyone relay service active", anyoneSub, node,
			"debros-anyone-relay is active", inspector.High))
	}

	// --- Client-mode checks ---
	if isClientMode {
		// SOCKS5 port
		if a.SocksListening {
			r = append(r, inspector.Pass("anyone.socks_listening", "SOCKS5 port 9050 listening", anyoneSub, node,
				"port 9050 bound", inspector.High))
		} else {
			r = append(r, inspector.Fail("anyone.socks_listening", "SOCKS5 port 9050 listening", anyoneSub, node,
				"port 9050 NOT bound (traffic cannot route through anonymity network)", inspector.High))
		}

		// Control port
		if a.ControlListening {
			r = append(r, inspector.Pass("anyone.control_listening", "Control port 9051 listening", anyoneSub, node,
				"port 9051 bound", inspector.Low))
		} else {
			r = append(r, inspector.Warn("anyone.control_listening", "Control port 9051 listening", anyoneSub, node,
				"port 9051 NOT bound (monitoring unavailable)", inspector.Low))
		}

		// Bootstrap (clients also bootstrap to the network)
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

	// --- Relay-mode checks ---

	// ORPort listening
	if a.ORPortListening {
		r = append(r, inspector.Pass("anyone.orport_listening", "ORPort 9001 listening", anyoneSub, node,
			"port 9001 bound", inspector.High))
	} else {
		r = append(r, inspector.Fail("anyone.orport_listening", "ORPort 9001 listening", anyoneSub, node,
			"port 9001 NOT bound", inspector.High))
	}

	// Control port
	if a.ControlListening {
		r = append(r, inspector.Pass("anyone.control_listening", "Control port 9051 listening", anyoneSub, node,
			"port 9051 bound", inspector.Low))
	} else {
		r = append(r, inspector.Warn("anyone.control_listening", "Control port 9051 listening", anyoneSub, node,
			"port 9051 NOT bound (monitoring unavailable)", inspector.Low))
	}

	// Bootstrap status
	if a.Bootstrapped {
		r = append(r, inspector.Pass("anyone.bootstrapped", "Relay bootstrapped", anyoneSub, node,
			fmt.Sprintf("bootstrap=%d%%", a.BootstrapPct), inspector.High))
	} else if a.BootstrapPct > 0 {
		r = append(r, inspector.Warn("anyone.bootstrapped", "Relay bootstrapped", anyoneSub, node,
			fmt.Sprintf("bootstrap=%d%% (still connecting)", a.BootstrapPct), inspector.High))
	} else {
		r = append(r, inspector.Fail("anyone.bootstrapped", "Relay bootstrapped", anyoneSub, node,
			"bootstrap=0% (not started or log missing)", inspector.High))
	}

	// Fingerprint present
	if a.Fingerprint != "" {
		r = append(r, inspector.Pass("anyone.fingerprint", "Relay has fingerprint", anyoneSub, node,
			fmt.Sprintf("fingerprint=%s", a.Fingerprint), inspector.Medium))
	} else {
		r = append(r, inspector.Warn("anyone.fingerprint", "Relay has fingerprint", anyoneSub, node,
			"no fingerprint found (relay may not have generated keys yet)", inspector.Medium))
	}

	// Nickname configured
	if a.Nickname != "" {
		r = append(r, inspector.Pass("anyone.nickname", "Relay nickname configured", anyoneSub, node,
			fmt.Sprintf("nickname=%s", a.Nickname), inspector.Low))
	} else {
		r = append(r, inspector.Warn("anyone.nickname", "Relay nickname configured", anyoneSub, node,
			"no nickname in /etc/anon/anonrc", inspector.Low))
	}

	// --- Legacy client checks (if also running client service) ---
	if a.ClientActive {
		r = append(r, inspector.Pass("anyone.client_active", "Anyone client service active", anyoneSub, node,
			"debros-anyone-client is active", inspector.High))

		if a.SocksListening {
			r = append(r, inspector.Pass("anyone.socks_listening", "SOCKS5 port 9050 listening", anyoneSub, node,
				"port 9050 bound", inspector.High))
		} else {
			r = append(r, inspector.Fail("anyone.socks_listening", "SOCKS5 port 9050 listening", anyoneSub, node,
				"port 9050 NOT bound", inspector.High))
		}
	}

	return r
}

func checkAnyoneCrossNode(data *inspector.ClusterData) []inspector.CheckResult {
	var r []inspector.CheckResult

	// ORPort reachability: only check from/to relay-mode nodes
	orportChecked := 0
	orportReachable := 0
	orportFailed := 0

	for _, nd := range data.Nodes {
		if nd.Anyone == nil {
			continue
		}
		for host, ok := range nd.Anyone.ORPortReachable {
			orportChecked++
			if ok {
				orportReachable++
			} else {
				orportFailed++
				r = append(r, inspector.Fail("anyone.orport_reachable",
					fmt.Sprintf("ORPort 9001 reachable on %s", host),
					anyoneSub, nd.Node.Name(),
					fmt.Sprintf("cannot TCP connect to %s:9001 from %s", host, nd.Node.Name()), inspector.High))
			}
		}
	}

	if orportChecked > 0 && orportFailed == 0 {
		r = append(r, inspector.Pass("anyone.orport_reachable", "ORPort 9001 reachable across nodes", anyoneSub, "",
			fmt.Sprintf("all %d cross-node connections OK", orportReachable), inspector.High))
	}

	return r
}
