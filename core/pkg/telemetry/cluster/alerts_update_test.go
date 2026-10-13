package cluster

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

func TestCheckNodeUpdate(t *testing.T) {
	notice := func(state string) *report.NodeReport {
		return &report.NodeReport{Update: &updatenotice.Notice{
			State: state, Mode: "notify", Channel: "stable", Current: "0.3.0", Candidate: "0.3.1", Reason: "why",
		}}
	}
	cases := []struct {
		name     string
		r        *report.NodeReport
		severity AlertSeverity
		contains string
	}{
		{"a newer release is information", notice(updatenotice.StateAvailable), AlertInfo, "Release 0.3.1 is available on the stable channel"},
		{"a refused release is a warning", notice(updatenotice.StateRefused), AlertWarning, "was refused on the stable channel: why"},
		{"a rolled-back install is a warning", notice(updatenotice.StateFailed), AlertWarning, "failed and was rolled back"},
	}
	for _, c := range cases {
		alerts := checkNodeUpdate(c.r, "203.0.113.5")
		if len(alerts) != 1 || alerts[0].Severity != c.severity || alerts[0].Subsystem != "update" ||
			alerts[0].Node != "203.0.113.5" || !strings.Contains(alerts[0].Message, c.contains) {
			t.Errorf("%s: %+v", c.name, alerts)
		}
	}
	if alerts := checkNodeUpdate(&report.NodeReport{}, "h"); len(alerts) != 0 {
		t.Errorf("a node with nothing to report raised %+v", alerts)
	}
	if alerts := checkNodeUpdate(&report.NodeReport{Update: &updatenotice.Notice{State: "unknown"}}, "h"); len(alerts) != 0 {
		t.Errorf("an unknown state raised %+v", alerts)
	}
}
