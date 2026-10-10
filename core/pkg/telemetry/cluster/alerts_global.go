package cluster

import (
	"github.com/DeBrosOfficial/network/pkg/telemetry/globalhealth"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func checkGlobalHealth(reports []*report.NodeReport) []Alert {
	nodes := make([]globalhealth.Node, 0, len(reports))
	for _, r := range reports {
		nodes = append(nodes, globalhealth.Node{Host: nodeHost(r), Chain: r.Chain, Global: r.Global})
	}
	issues := globalhealth.Evaluate(nodes)
	alerts := make([]Alert, 0, len(issues))
	for _, issue := range issues {
		sev := AlertWarning
		if issue.Severity == globalhealth.Critical {
			sev = AlertCritical
		}
		alerts = append(alerts, Alert{sev, issue.Subsystem, issue.Host, issue.Message})
	}
	return alerts
}
