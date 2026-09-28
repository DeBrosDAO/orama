package checks

import (
	"sort"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/telemetry/globalhealth"
)

func init() {
	inspector.RegisterChecker("global", CheckGlobal)
}

const globalSub = "global"

// CheckGlobal checks the chain and the global-node services from the facts
// collectGlobalNode gathered. A node with neither section is skipped.
func CheckGlobal(data *inspector.ClusterData) []inspector.CheckResult {
	if data == nil {
		return nil
	}
	hosts := make([]string, 0, len(data.Nodes))
	nodes := make([]globalhealth.Node, 0, len(data.Nodes))
	for host, nd := range data.Nodes {
		if nd == nil || (nd.Chain == nil && nd.Global == nil) {
			continue
		}
		hosts = append(hosts, host)
		nodes = append(nodes, globalhealth.Node{Host: host, Chain: nd.Chain, Global: nd.Global})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Host < nodes[j].Host })
	sort.Strings(hosts)

	issues := globalhealth.Evaluate(nodes)
	byHost := map[string][]globalhealth.Issue{}
	for _, issue := range issues {
		byHost[issue.Host] = append(byHost[issue.Host], issue)
	}

	var results []inspector.CheckResult
	for _, host := range hosts {
		nd := data.Nodes[host]
		results = append(results, sectionResults(host, "chain", nd.Chain != nil, byHost[host])...)
		results = append(results, sectionResults(host, globalSub, nd.Global != nil, byHost[host])...)
	}
	return results
}

func sectionResults(host, subsystem string, present bool, issues []globalhealth.Issue) []inspector.CheckResult {
	if !present {
		return nil
	}
	var out []inspector.CheckResult
	for _, issue := range issues {
		if issue.Subsystem != subsystem {
			continue
		}
		sev := inspector.High
		status := inspector.StatusWarn
		if issue.Severity == globalhealth.Critical {
			sev = inspector.Critical
			status = inspector.StatusFail
		}
		out = append(out, inspector.CheckResult{
			ID: issue.Code, Name: issue.Code, Subsystem: subsystem, Severity: sev,
			Status: status, Message: issue.Message, Node: host,
		})
	}
	if len(out) == 0 {
		id := subsystem + ".healthy"
		out = append(out, inspector.Pass(id, subsystem+" within limits", subsystem, host, "no alert", inspector.High))
	}
	return out
}
