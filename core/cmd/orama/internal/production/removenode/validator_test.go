package removenode

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func snapshotOf(host string, rep *report.NodeReport) *cluster.ClusterSnapshot {
	return &cluster.ClusterSnapshot{Nodes: []cluster.CollectionStatus{{Node: cluster.NodeRef{Host: host}, Report: rep}}}
}

func TestValidatorStatus(t *testing.T) {
	tests := []struct {
		name          string
		rep           *report.NodeReport
		isValidator   bool
		known         bool
		reportedHosts string
	}{
		{"a validator", &report.NodeReport{Chain: &report.ChainReport{Responsive: true, IsValidator: true}}, true, true, "10.0.0.3"},
		{"not a validator", &report.NodeReport{Chain: &report.ChainReport{Responsive: true}}, false, true, "10.0.0.3"},
		{"no report from the node", nil, false, false, "10.0.0.4"},
		{"a report with no chain section", &report.NodeReport{}, false, false, "10.0.0.3"},
		{"a chain that did not answer the collector", &report.NodeReport{Chain: &report.ChainReport{Responsive: false}}, false, false, "10.0.0.3"},
		{"a chain section that carries an error", &report.NodeReport{Chain: &report.ChainReport{Responsive: true, Error: "status: connection refused"}}, false, false, "10.0.0.3"},
		{"a crashed validator, IsValidator unread", &report.NodeReport{Chain: &report.ChainReport{Error: "rpc down", IsValidator: false}}, false, false, "10.0.0.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := snapshotOf("10.0.0.3", tt.rep)

			isValidator, known := validatorStatus(snap, tt.reportedHosts)

			if isValidator != tt.isValidator || known != tt.known {
				t.Errorf("validatorStatus = %v, %v; want %v, %v", isValidator, known, tt.isValidator, tt.known)
			}
		})
	}
}
