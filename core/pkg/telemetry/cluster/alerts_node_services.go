package cluster

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// checkNodeTor alerts on the node's Tor client beyond its unit state, which
// checkNodeServices already covers: a SOCKS port that is not bound, a client
// that has not finished bootstrapping, and Anyone leftovers from an
// incomplete migration.
func checkNodeTor(r *report.NodeReport, host string) []Alert {
	if r.Tor == nil {
		return nil
	}
	var alerts []Alert
	t := r.Tor
	if t.ClientActive && !t.SocksListening {
		alerts = append(alerts, Alert{AlertWarning, "tor", host,
			fmt.Sprintf("Tor active but SOCKS port %d not bound (/v1/proxy/anon is down)", constants.TorSOCKSPort)})
	}
	// A negative percentage means the journal no longer says; not an alert.
	if t.ClientActive && t.BootstrapPct >= 0 && !t.Bootstrapped {
		alerts = append(alerts, Alert{AlertWarning, "tor", host,
			fmt.Sprintf("Tor bootstrap at %d%%", t.BootstrapPct)})
	}
	if t.LegacyAnyone {
		alerts = append(alerts, Alert{AlertWarning, "tor", host,
			"Anyone network still present; run `orama node upgrade` on this node"})
	}
	return alerts
}

func checkNodeProcesses(r *report.NodeReport, host string) []Alert {
	if r.Processes == nil {
		return nil
	}
	var alerts []Alert
	if r.Processes.ZombieCount > 0 {
		alerts = append(alerts, Alert{AlertInfo, "system", host,
			fmt.Sprintf("%d zombie processes", r.Processes.ZombieCount)})
	}
	if r.Processes.OrphanCount > 0 {
		alerts = append(alerts, Alert{AlertInfo, "system", host,
			fmt.Sprintf("%d orphan orama processes", r.Processes.OrphanCount)})
	}
	if r.Processes.PanicCount > 0 {
		alerts = append(alerts, Alert{AlertCritical, "system", host,
			fmt.Sprintf("%d panic/fatal in orama-node logs (1h)", r.Processes.PanicCount)})
	}
	return alerts
}

func checkNodeNamespaces(r *report.NodeReport, host string) []Alert {
	var alerts []Alert
	for _, ns := range r.Namespaces {
		if !ns.GatewayUp {
			alerts = append(alerts, Alert{AlertWarning, "namespace", host,
				fmt.Sprintf("Namespace %s gateway down", ns.Name)})
		}
		if !ns.RQLiteUp {
			alerts = append(alerts, Alert{AlertWarning, "namespace", host,
				fmt.Sprintf("Namespace %s RQLite down", ns.Name)})
		}
	}
	return alerts
}

func checkNodeNetwork(r *report.NodeReport, host string) []Alert {
	if r.Network == nil {
		return nil
	}
	var alerts []Alert
	if !r.Network.UFWActive {
		alerts = append(alerts, Alert{AlertCritical, "network", host, "UFW firewall is inactive"})
	}
	if !r.Network.InternetReachable {
		alerts = append(alerts, Alert{AlertWarning, "network", host, "Internet not reachable (ping 8.8.8.8 failed)"})
	}
	if r.Network.TCPRetransRate > 5.0 {
		alerts = append(alerts, Alert{AlertWarning, "network", host,
			fmt.Sprintf("High TCP retransmission rate: %.1f%%", r.Network.TCPRetransRate)})
	}

	// Check for internal ports exposed in UFW rules. Index internals are
	// reachable over the WireGuard overlay and localhost only; a UFW ALLOW that
	// is not scoped to the overlay subnet exposes them to the internet.
	internalPorts := []string{
		strconv.Itoa(constants.RQLiteHTTPPort),
		strconv.Itoa(constants.RQLiteRaftPort),
		strconv.Itoa(constants.OlricHTTPPort),
		strconv.Itoa(constants.GatewayAPIPort),
		strconv.Itoa(constants.IPFSAPIPort),
		strconv.Itoa(constants.IPFSClusterAPIPort),
	}
	for _, rule := range r.Network.UFWRules {
		ruleLower := strings.ToLower(rule)
		// Only flag ALLOW rules (not deny/reject).
		if !strings.Contains(ruleLower, "allow") {
			continue
		}
		for _, port := range internalPorts {
			// Match rules like "10100 ALLOW Anywhere" or "10100/tcp ALLOW IN"
			// but not rules restricted to 10.0.0.0/24 (WG subnet).
			if strings.Contains(rule, port) && !strings.Contains(rule, "10.0.0.") {
				alerts = append(alerts, Alert{AlertCritical, "network", host,
					fmt.Sprintf("Internal port %s exposed in UFW: %s", port, strings.TrimSpace(rule))})
			}
		}
	}

	return alerts
}

func checkNodeOlric(r *report.NodeReport, host string) []Alert {
	if r.Olric == nil {
		return nil
	}
	var alerts []Alert

	if !r.Olric.ServiceActive {
		alerts = append(alerts, Alert{AlertCritical, "olric", host, "Olric service down"})
		return alerts
	}
	if !r.Olric.MemberlistUp {
		alerts = append(alerts, Alert{AlertCritical, "olric", host, "Olric memberlist port down"})
	}
	if r.Olric.LogSuspects > 0 {
		alerts = append(alerts, Alert{AlertWarning, "olric", host,
			fmt.Sprintf("Olric member suspects: %d in last hour", r.Olric.LogSuspects)})
	}
	if r.Olric.LogFlapping > 5 {
		alerts = append(alerts, Alert{AlertWarning, "olric", host,
			fmt.Sprintf("Olric members flapping: %d join/leave events in last hour", r.Olric.LogFlapping)})
	}
	if r.Olric.LogErrors > 20 {
		alerts = append(alerts, Alert{AlertWarning, "olric", host,
			fmt.Sprintf("High Olric error rate: %d errors in last hour", r.Olric.LogErrors)})
	}
	if r.Olric.RestartCount > 3 {
		alerts = append(alerts, Alert{AlertWarning, "olric", host,
			fmt.Sprintf("Olric excessive restarts: %d", r.Olric.RestartCount)})
	}
	if r.Olric.ProcessMemMB > 500 {
		alerts = append(alerts, Alert{AlertWarning, "olric", host,
			fmt.Sprintf("Olric high memory: %dMB", r.Olric.ProcessMemMB)})
	}

	return alerts
}

func checkNodeIPFS(r *report.NodeReport, host string) []Alert {
	if r.IPFS == nil {
		return nil
	}
	var alerts []Alert

	if !r.IPFS.DaemonActive {
		alerts = append(alerts, Alert{AlertCritical, "ipfs", host, "IPFS daemon down"})
	}
	if !r.IPFS.ClusterActive {
		alerts = append(alerts, Alert{AlertCritical, "ipfs", host, "IPFS cluster down"})
	}

	// Only check these if daemon is running (otherwise data is meaningless).
	if r.IPFS.DaemonActive {
		if r.IPFS.SwarmPeerCount == 0 {
			alerts = append(alerts, Alert{AlertCritical, "ipfs", host, "IPFS isolated: no swarm peers"})
		}
		if !r.IPFS.HasSwarmKey {
			alerts = append(alerts, Alert{AlertCritical, "ipfs", host,
				"IPFS swarm key missing (private network compromised)"})
		}
		if !r.IPFS.BootstrapEmpty {
			alerts = append(alerts, Alert{AlertWarning, "ipfs", host,
				"IPFS bootstrap list not empty (should be empty for private swarm)"})
		}
	}

	if r.IPFS.RepoUsePct > 95 {
		alerts = append(alerts, Alert{AlertCritical, "ipfs", host,
			fmt.Sprintf("IPFS repo nearly full: %d%%", r.IPFS.RepoUsePct)})
	} else if r.IPFS.RepoUsePct > 90 {
		alerts = append(alerts, Alert{AlertWarning, "ipfs", host,
			fmt.Sprintf("IPFS repo at %d%%", r.IPFS.RepoUsePct)})
	}

	if r.IPFS.ClusterErrors > 0 {
		alerts = append(alerts, Alert{AlertWarning, "ipfs", host,
			fmt.Sprintf("IPFS cluster peer errors: %d", r.IPFS.ClusterErrors)})
	}

	if age := time.Duration(r.IPFS.OldestPinLockAgeSeconds) * time.Second; age > ipfsPinLockStallAge {
		alerts = append(alerts, Alert{AlertWarning, "ipfs", host,
			fmt.Sprintf("IPFS %s active for %s: it holds or waits for Kubo's pin lock, so repo GC cannot finish",
				r.IPFS.OldestPinLockCmd, age.Round(time.Minute))})
	}

	return alerts
}

// ipfsPinLockStallAge is how long a pin/add, pin/update or repo/gc may stay
// active before it is reported. It is the repo GC unit's TimeoutStartSec
// (orama-namespace-ipfs-gc@.service): a request that has held Kubo's pin lock
// this long outlasts a whole GC run, which then times out having freed
// nothing.
const ipfsPinLockStallAge = 30 * time.Minute

func checkNodeVault(r *report.NodeReport, host string) []Alert {
	if r.Vault == nil {
		return nil
	}
	var alerts []Alert

	if !r.Vault.ServiceActive {
		alerts = append(alerts, Alert{AlertCritical, "vault", host, "Vault service not running"})
		return alerts
	}

	if !r.Vault.Responsive {
		alerts = append(alerts, Alert{AlertWarning, "vault", host, "Vault not responding to health queries"})
		return alerts
	}

	switch r.Vault.Status {
	case "unavailable":
		alerts = append(alerts, Alert{AlertCritical, "vault", host,
			fmt.Sprintf("Vault unavailable: %d/%d guardians healthy (need %d for reads)",
				r.Vault.Healthy, r.Vault.Guardians, r.Vault.Threshold)})
	case "degraded":
		alerts = append(alerts, Alert{AlertWarning, "vault", host,
			fmt.Sprintf("Vault degraded: %d/%d guardians healthy (need %d for writes)",
				r.Vault.Healthy, r.Vault.Guardians, r.Vault.WriteQuorum)})
	}

	if r.Vault.RestartCount > 3 {
		alerts = append(alerts, Alert{AlertWarning, "vault", host,
			fmt.Sprintf("Vault restarted %d times", r.Vault.RestartCount)})
	}

	return alerts
}

func checkNodeGateway(r *report.NodeReport, host string) []Alert {
	if r.Gateway == nil {
		return nil
	}
	var alerts []Alert

	if !r.Gateway.Responsive {
		alerts = append(alerts, Alert{AlertCritical, "gateway", host, "Gateway not responding"})
		return alerts
	}

	if r.Gateway.HTTPStatus != 200 {
		alerts = append(alerts, Alert{AlertWarning, "gateway", host,
			fmt.Sprintf("Gateway health check returned HTTP %d", r.Gateway.HTTPStatus)})
	}

	for name, sub := range r.Gateway.Subsystems {
		if sub.Status != "ok" && sub.Status != "" {
			msg := fmt.Sprintf("Gateway subsystem %s: status=%s", name, sub.Status)
			if sub.Error != "" {
				msg += fmt.Sprintf(" error=%s", sub.Error)
			}
			alerts = append(alerts, Alert{AlertWarning, "gateway", host, msg})
		}
	}

	return alerts
}

func truncateKey(key string) string {
	if len(key) > 8 {
		return key[:8] + "..."
	}
	return key
}
