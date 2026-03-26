package checks

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("network", CheckNetwork)
}

const networkSub = "network"

// CheckNetwork runs all network-level health checks.
func CheckNetwork(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	for _, nd := range data.Nodes {
		if nd.Network == nil {
			continue
		}
		results = append(results, checkNetworkPerNode(nd)...)
	}

	return results
}

func checkNetworkPerNode(nd *inspector.NodeData) []inspector.CheckResult {
	var r []inspector.CheckResult
	net := nd.Network
	node := nd.Node.Name()

	// 7.2 Internet connectivity
	if net.InternetReachable {
		r = append(r, inspector.Pass("network.internet", "Internet reachable (ping 8.8.8.8)", networkSub, node,
			"ping 8.8.8.8 succeeded", inspector.High))
	} else {
		r = append(r, inspector.Fail("network.internet", "Internet reachable (ping 8.8.8.8)", networkSub, node,
			"ping 8.8.8.8 failed", inspector.High))
	}

	// 7.14 Default route
	if net.DefaultRoute {
		r = append(r, inspector.Pass("network.default_route", "Default route exists", networkSub, node,
			"default route present", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("network.default_route", "Default route exists", networkSub, node,
			"no default route", inspector.Critical))
	}

	// 7.15 WG subnet route
	if net.WGRouteExists {
		r = append(r, inspector.Pass("network.wg_route", "WG subnet route exists", networkSub, node,
			"10.0.0.0/24 via wg0 present", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("network.wg_route", "WG subnet route exists", networkSub, node,
			"10.0.0.0/24 route via wg0 NOT found", inspector.Critical))
	}

	// 7.4 TCP connections
	if net.TCPEstablished > 0 {
		if net.TCPEstablished < 5000 {
			r = append(r, inspector.Pass("network.tcp_established", "TCP connections reasonable", networkSub, node,
				fmt.Sprintf("established=%d", net.TCPEstablished), inspector.Medium))
		} else {
			r = append(r, inspector.Warn("network.tcp_established", "TCP connections reasonable", networkSub, node,
				fmt.Sprintf("established=%d (high)", net.TCPEstablished), inspector.Medium))
		}
	}

	// 7.6 TIME_WAIT
	if net.TCPTimeWait < 10000 {
		r = append(r, inspector.Pass("network.tcp_timewait", "TIME_WAIT count low", networkSub, node,
			fmt.Sprintf("timewait=%d", net.TCPTimeWait), inspector.Medium))
	} else {
		r = append(r, inspector.Warn("network.tcp_timewait", "TIME_WAIT count low", networkSub, node,
			fmt.Sprintf("timewait=%d (accumulating)", net.TCPTimeWait), inspector.Medium))
	}

	// 7.8 TCP retransmission rate
	// Thresholds are relaxed for WireGuard-encapsulated traffic across VPS providers:
	// <2% normal, 2-10% elevated (warn), >=10% problematic (fail).
	if net.TCPRetransRate >= 0 {
		if net.TCPRetransRate < 2 {
			r = append(r, inspector.Pass("network.tcp_retrans", "TCP retransmission rate low", networkSub, node,
				fmt.Sprintf("retrans=%.2f%%", net.TCPRetransRate), inspector.Medium))
		} else if net.TCPRetransRate < 10 {
			r = append(r, inspector.Warn("network.tcp_retrans", "TCP retransmission rate low", networkSub, node,
				fmt.Sprintf("retrans=%.2f%% (elevated)", net.TCPRetransRate), inspector.Medium))
		} else {
			r = append(r, inspector.Fail("network.tcp_retrans", "TCP retransmission rate low", networkSub, node,
				fmt.Sprintf("retrans=%.2f%% (high packet loss)", net.TCPRetransRate), inspector.High))
		}
	}

	// 7.10 WG mesh peer pings (NxN connectivity)
	if len(net.PingResults) > 0 {
		failCount := 0
		for _, ok := range net.PingResults {
			if !ok {
				failCount++
			}
		}
		if failCount == 0 {
			r = append(r, inspector.Pass("network.wg_mesh_ping", "All WG peers reachable via ping", networkSub, node,
				fmt.Sprintf("%d/%d peers pingable", len(net.PingResults), len(net.PingResults)), inspector.Critical))
		} else {
			r = append(r, inspector.Fail("network.wg_mesh_ping", "All WG peers reachable via ping", networkSub, node,
				fmt.Sprintf("%d/%d peers unreachable", failCount, len(net.PingResults)), inspector.Critical))
		}
	}

	return r
}
