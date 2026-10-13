package checks

import (
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("wireguard", CheckWireGuard)
}

const wgSub = "wireguard"

// CheckWireGuard runs all WireGuard health checks.
func CheckWireGuard(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	for _, nd := range data.Nodes {
		if nd.WireGuard == nil {
			continue
		}
		results = append(results, checkWGPerNode(nd, data)...)
	}

	results = append(results, checkWGCrossNode(data)...)

	return results
}

func checkWGPerNode(nd *inspector.NodeData, data *inspector.ClusterData) []inspector.CheckResult {
	var r []inspector.CheckResult
	wg := nd.WireGuard
	node := nd.Node.Name()

	// 5.1 Interface up
	if wg.InterfaceUp {
		r = append(r, inspector.Pass("wg.interface_up", "WireGuard interface up", wgSub, node,
			fmt.Sprintf("wg0 up, IP=%s", wg.WgIP), inspector.Critical))
	} else {
		r = append(r, inspector.Fail("wg.interface_up", "WireGuard interface up", wgSub, node,
			"wg0 interface is DOWN", inspector.Critical))
		return r
	}

	// 5.2 Service active
	if wg.ServiceActive {
		r = append(r, inspector.Pass("wg.service_active", "wireguard @index service active", wgSub, node,
			"service is active", inspector.Critical))
	} else {
		r = append(r, inspector.Warn("wg.service_active", "wireguard @index service active", wgSub, node,
			"service not active (interface up but service not managed by systemd?)", inspector.High))
	}

	// 5.5 Correct IP in 10.0.0.0/24
	if wg.WgIP != "" && strings.HasPrefix(wg.WgIP, "10.0.0.") {
		r = append(r, inspector.Pass("wg.correct_ip", "WG IP in expected range", wgSub, node,
			fmt.Sprintf("IP=%s (10.0.0.0/24)", wg.WgIP), inspector.Critical))
	} else if wg.WgIP != "" {
		r = append(r, inspector.Warn("wg.correct_ip", "WG IP in expected range", wgSub, node,
			fmt.Sprintf("IP=%s (not in 10.0.0.0/24)", wg.WgIP), inspector.High))
	}

	// 5.4 Listen port
	if wg.ListenPort == 51820 {
		r = append(r, inspector.Pass("wg.listen_port", "Listen port is 51820", wgSub, node,
			"port=51820", inspector.Critical))
	} else if wg.ListenPort > 0 {
		r = append(r, inspector.Warn("wg.listen_port", "Listen port is 51820", wgSub, node,
			fmt.Sprintf("port=%d (expected 51820)", wg.ListenPort), inspector.High))
	}

	// 5.7 Peer count
	expectedNodes := countWGNodes(data)
	expectedPeers := expectedNodes - 1
	if expectedPeers < 0 {
		expectedPeers = 0
	}
	if wg.PeerCount >= expectedPeers {
		r = append(r, inspector.Pass("wg.peer_count", "Peer count matches expected", wgSub, node,
			fmt.Sprintf("peers=%d (expected=%d)", wg.PeerCount, expectedPeers), inspector.High))
	} else if wg.PeerCount > 0 {
		r = append(r, inspector.Warn("wg.peer_count", "Peer count matches expected", wgSub, node,
			fmt.Sprintf("peers=%d (expected=%d)", wg.PeerCount, expectedPeers), inspector.High))
	} else {
		r = append(r, inspector.Fail("wg.peer_count", "Peer count matches expected", wgSub, node,
			fmt.Sprintf("peers=%d (isolated!)", wg.PeerCount), inspector.Critical))
	}

	// 5.29 MTU
	if wg.MTU == 1420 {
		r = append(r, inspector.Pass("wg.mtu", "MTU is 1420", wgSub, node,
			"MTU=1420", inspector.High))
	} else if wg.MTU > 0 {
		r = append(r, inspector.Warn("wg.mtu", "MTU is 1420", wgSub, node,
			fmt.Sprintf("MTU=%d (expected 1420)", wg.MTU), inspector.High))
	}

	// 5.35 Config file exists
	if wg.ConfigExists {
		r = append(r, inspector.Pass("wg.config_exists", "Config file exists", wgSub, node,
			"/etc/wireguard/wg0.conf present", inspector.High))
	} else {
		r = append(r, inspector.Warn("wg.config_exists", "Config file exists", wgSub, node,
			"/etc/wireguard/wg0.conf NOT found", inspector.High))
	}

	// 5.36 Config permissions
	if wg.ConfigPerms == "600" {
		r = append(r, inspector.Pass("wg.config_perms", "Config file permissions 600", wgSub, node,
			"perms=600", inspector.Critical))
	} else if wg.ConfigPerms != "" && wg.ConfigPerms != "000" {
		r = append(r, inspector.Warn("wg.config_perms", "Config file permissions 600", wgSub, node,
			fmt.Sprintf("perms=%s (expected 600)", wg.ConfigPerms), inspector.Critical))
	}

	// Per-peer checks
	now := time.Now().Unix()
	neverHandshaked := 0
	staleHandshakes := 0
	noTraffic := 0

	for _, peer := range wg.Peers {
		// 5.20 Each peer has exactly one /32 allowed IP
		if !strings.Contains(peer.AllowedIPs, "/32") {
			r = append(r, inspector.Warn("wg.peer_allowed_ip", "Peer has /32 allowed IP", wgSub, node,
				fmt.Sprintf("peer %s...%s has allowed_ips=%s", peer.PublicKey[:8], peer.PublicKey[len(peer.PublicKey)-4:], peer.AllowedIPs), inspector.High))
		}

		// 5.23 No peer has 0.0.0.0/0
		if strings.Contains(peer.AllowedIPs, "0.0.0.0/0") {
			r = append(r, inspector.Fail("wg.peer_catch_all", "No catch-all route peer", wgSub, node,
				fmt.Sprintf("peer %s...%s has 0.0.0.0/0 (route hijack!)", peer.PublicKey[:8], peer.PublicKey[len(peer.PublicKey)-4:]), inspector.Critical))
		}

		// 5.11-5.12 Handshake freshness
		if peer.LatestHandshake == 0 {
			neverHandshaked++
		} else {
			age := now - peer.LatestHandshake
			if age > 300 {
				staleHandshakes++
			}
		}

		// 5.13 Transfer stats
		if peer.TransferRx == 0 && peer.TransferTx == 0 {
			noTraffic++
		}
	}

	if len(wg.Peers) > 0 {
		// 5.12 Never handshaked
		if neverHandshaked == 0 {
			r = append(r, inspector.Pass("wg.handshake_all", "All peers have handshaked", wgSub, node,
				fmt.Sprintf("%d/%d peers handshaked", len(wg.Peers), len(wg.Peers)), inspector.Critical))
		} else {
			r = append(r, inspector.Fail("wg.handshake_all", "All peers have handshaked", wgSub, node,
				fmt.Sprintf("%d/%d peers never handshaked", neverHandshaked, len(wg.Peers)), inspector.Critical))
		}

		// 5.11 Stale handshakes
		if staleHandshakes == 0 {
			r = append(r, inspector.Pass("wg.handshake_fresh", "All handshakes recent (<5m)", wgSub, node,
				"all handshakes within 5 minutes", inspector.High))
		} else {
			r = append(r, inspector.Warn("wg.handshake_fresh", "All handshakes recent (<5m)", wgSub, node,
				fmt.Sprintf("%d/%d peers with stale handshake (>5m)", staleHandshakes, len(wg.Peers)), inspector.High))
		}

		// 5.13 Transfer
		if noTraffic == 0 {
			r = append(r, inspector.Pass("wg.peer_traffic", "All peers have traffic", wgSub, node,
				fmt.Sprintf("%d/%d peers with traffic", len(wg.Peers), len(wg.Peers)), inspector.High))
		} else {
			r = append(r, inspector.Warn("wg.peer_traffic", "All peers have traffic", wgSub, node,
				fmt.Sprintf("%d/%d peers with zero traffic", noTraffic, len(wg.Peers)), inspector.High))
		}
	}

	return r
}

func checkWGCrossNode(data *inspector.ClusterData) []inspector.CheckResult {
	var r []inspector.CheckResult

	type nodeInfo struct {
		name string
		wg   *inspector.WireGuardData
	}
	var nodes []nodeInfo
	for _, nd := range data.Nodes {
		if nd.WireGuard != nil && nd.WireGuard.InterfaceUp {
			nodes = append(nodes, nodeInfo{name: nd.Node.Name(), wg: nd.WireGuard})
		}
	}

	if len(nodes) < 2 {
		return r
	}

	// 5.8 Peer count consistent
	counts := map[int]int{}
	for _, n := range nodes {
		counts[n.wg.PeerCount]++
	}
	if len(counts) == 1 {
		for c := range counts {
			r = append(r, inspector.Pass("wg.peer_count_consistent", "Peer count consistent across nodes", wgSub, "",
				fmt.Sprintf("all nodes have %d peers", c), inspector.High))
		}
	} else {
		var parts []string
		for c, num := range counts {
			parts = append(parts, fmt.Sprintf("%d nodes have %d peers", num, c))
		}
		r = append(r, inspector.Warn("wg.peer_count_consistent", "Peer count consistent across nodes", wgSub, "",
			strings.Join(parts, "; "), inspector.High))
	}

	// 5.30 MTU consistent
	mtus := map[int]int{}
	for _, n := range nodes {
		if n.wg.MTU > 0 {
			mtus[n.wg.MTU]++
		}
	}
	if len(mtus) == 1 {
		for m := range mtus {
			r = append(r, inspector.Pass("wg.mtu_consistent", "MTU consistent across nodes", wgSub, "",
				fmt.Sprintf("all nodes MTU=%d", m), inspector.High))
		}
	} else if len(mtus) > 1 {
		r = append(r, inspector.Warn("wg.mtu_consistent", "MTU consistent across nodes", wgSub, "",
			fmt.Sprintf("%d different MTU values", len(mtus)), inspector.High))
	}

	// 5.50 Public key uniqueness
	allKeys := map[string][]string{}
	for _, n := range nodes {
		for _, peer := range n.wg.Peers {
			allKeys[peer.PublicKey] = append(allKeys[peer.PublicKey], n.name)
		}
	}
	dupeKeys := 0
	for _, names := range allKeys {
		if len(names) > len(nodes)-1 {
			dupeKeys++
		}
	}
	// If all good, the same key should appear at most N-1 times (once per other node)
	if dupeKeys == 0 {
		r = append(r, inspector.Pass("wg.key_uniqueness", "Public keys unique across nodes", wgSub, "",
			fmt.Sprintf("%d unique peer keys", len(allKeys)), inspector.Critical))
	}

	return r
}

func countWGNodes(data *inspector.ClusterData) int {
	count := 0
	for _, nd := range data.Nodes {
		if nd.WireGuard != nil {
			count++
		}
	}
	return count
}
