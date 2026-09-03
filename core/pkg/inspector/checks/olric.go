package checks

import (
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func init() {
	inspector.RegisterChecker("olric", CheckOlric)
}

const olricSub = "olric"

// CheckOlric runs all Olric health checks against cluster data.
func CheckOlric(data *inspector.ClusterData) []inspector.CheckResult {
	var results []inspector.CheckResult

	for _, nd := range data.Nodes {
		if nd.Olric == nil {
			continue
		}
		results = append(results, checkOlricPerNode(nd)...)
	}

	results = append(results, checkOlricCrossNode(data)...)

	return results
}

func checkOlricPerNode(nd *inspector.NodeData) []inspector.CheckResult {
	var r []inspector.CheckResult
	ol := nd.Olric
	node := nd.Node.Name()

	// 2.1 Service active
	if ol.ServiceActive {
		r = append(r, inspector.Pass("olric.service_active", "Olric service active", olricSub, node,
			"orama-namespace-olric@index is active", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("olric.service_active", "Olric service active", olricSub, node,
			"orama-namespace-olric@index is not active", inspector.Critical))
		return r
	}

	// 2.7 Memberlist port accepting connections
	if ol.MemberlistUp {
		r = append(r, inspector.Pass("olric.memberlist_port", "Memberlist port 3322 listening", olricSub, node,
			"TCP 3322 is bound", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("olric.memberlist_port", "Memberlist port 3322 listening", olricSub, node,
			"TCP 3322 is not listening", inspector.Critical))
	}

	// 2.3 Restart count
	if ol.RestartCount == 0 {
		r = append(r, inspector.Pass("olric.restarts", "Low restart count", olricSub, node,
			"NRestarts=0", inspector.High))
	} else if ol.RestartCount <= 3 {
		r = append(r, inspector.Warn("olric.restarts", "Low restart count", olricSub, node,
			fmt.Sprintf("NRestarts=%d", ol.RestartCount), inspector.High))
	} else {
		r = append(r, inspector.Fail("olric.restarts", "Low restart count", olricSub, node,
			fmt.Sprintf("NRestarts=%d (crash-looping?)", ol.RestartCount), inspector.High))
	}

	// 2.4 Process memory
	if ol.ProcessMemMB > 0 {
		if ol.ProcessMemMB < 200 {
			r = append(r, inspector.Pass("olric.memory", "Process memory healthy", olricSub, node,
				fmt.Sprintf("RSS=%dMB", ol.ProcessMemMB), inspector.Medium))
		} else if ol.ProcessMemMB < 500 {
			r = append(r, inspector.Warn("olric.memory", "Process memory healthy", olricSub, node,
				fmt.Sprintf("RSS=%dMB (elevated)", ol.ProcessMemMB), inspector.Medium))
		} else {
			r = append(r, inspector.Fail("olric.memory", "Process memory healthy", olricSub, node,
				fmt.Sprintf("RSS=%dMB (high)", ol.ProcessMemMB), inspector.High))
		}
	}

	// 2.9-2.11 Log analysis: suspects
	if ol.LogSuspects == 0 {
		r = append(r, inspector.Pass("olric.log_suspects", "No suspect/failed members in logs", olricSub, node,
			"no suspect messages in last hour", inspector.Critical))
	} else {
		r = append(r, inspector.Fail("olric.log_suspects", "No suspect/failed members in logs", olricSub, node,
			fmt.Sprintf("%d suspect/failed messages in last hour", ol.LogSuspects), inspector.Critical))
	}

	// 2.13 Flapping detection
	if ol.LogFlapping < 5 {
		r = append(r, inspector.Pass("olric.log_flapping", "No rapid join/leave cycles", olricSub, node,
			fmt.Sprintf("join/leave events=%d in last hour", ol.LogFlapping), inspector.High))
	} else {
		r = append(r, inspector.Warn("olric.log_flapping", "No rapid join/leave cycles", olricSub, node,
			fmt.Sprintf("join/leave events=%d in last hour (flapping?)", ol.LogFlapping), inspector.High))
	}

	// 2.39 Log error rate
	if ol.LogErrors < 5 {
		r = append(r, inspector.Pass("olric.log_errors", "Log error rate low", olricSub, node,
			fmt.Sprintf("errors=%d in last hour", ol.LogErrors), inspector.High))
	} else if ol.LogErrors < 20 {
		r = append(r, inspector.Warn("olric.log_errors", "Log error rate low", olricSub, node,
			fmt.Sprintf("errors=%d in last hour", ol.LogErrors), inspector.High))
	} else {
		r = append(r, inspector.Fail("olric.log_errors", "Log error rate low", olricSub, node,
			fmt.Sprintf("errors=%d in last hour (high)", ol.LogErrors), inspector.High))
	}

	return r
}

func checkOlricCrossNode(data *inspector.ClusterData) []inspector.CheckResult {
	var r []inspector.CheckResult

	activeCount := 0
	memberlistCount := 0
	totalNodes := 0

	for _, nd := range data.Nodes {
		if nd.Olric == nil {
			continue
		}
		totalNodes++
		if nd.Olric.ServiceActive {
			activeCount++
		}
		if nd.Olric.MemberlistUp {
			memberlistCount++
		}
	}

	if totalNodes < 2 {
		return r
	}

	// All nodes have Olric running
	if activeCount == totalNodes {
		r = append(r, inspector.Pass("olric.all_active", "All nodes running Olric", olricSub, "",
			fmt.Sprintf("%d/%d nodes active", activeCount, totalNodes), inspector.Critical))
	} else {
		r = append(r, inspector.Fail("olric.all_active", "All nodes running Olric", olricSub, "",
			fmt.Sprintf("%d/%d nodes active", activeCount, totalNodes), inspector.Critical))
	}

	// All memberlist ports up
	if memberlistCount == totalNodes {
		r = append(r, inspector.Pass("olric.all_memberlist", "All memberlist ports listening", olricSub, "",
			fmt.Sprintf("%d/%d nodes with memberlist", memberlistCount, totalNodes), inspector.High))
	} else {
		r = append(r, inspector.Warn("olric.all_memberlist", "All memberlist ports listening", olricSub, "",
			fmt.Sprintf("%d/%d nodes with memberlist", memberlistCount, totalNodes), inspector.High))
	}

	return r
}
