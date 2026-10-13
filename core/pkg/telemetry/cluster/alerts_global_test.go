package cluster

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func TestCheckGlobalHealth_jailedIsCritical(t *testing.T) {
	jailed := true
	r := &report.NodeReport{
		PublicIP: "10.0.0.4",
		Chain: &report.ChainReport{
			ServiceActive: true, UnitState: "active", Responsive: true,
			LatestHeight: 10, Peers: 2, ValidatorCount: 3, BlockAgeSec: 1,
			Jailed: &jailed,
		},
	}
	alerts := checkGlobalHealth([]*report.NodeReport{r})
	if len(alerts) != 1 || alerts[0].Severity != AlertCritical || alerts[0].Subsystem != "chain" || alerts[0].Node != "10.0.0.4" {
		t.Fatalf("got %+v", alerts)
	}
}
