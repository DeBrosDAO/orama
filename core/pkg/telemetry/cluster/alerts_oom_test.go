package cluster

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func oomAlerts(sys *report.SystemReport) []Alert {
	return checkNodeSystem(&report.NodeReport{System: sys}, "n1")
}

func TestCheckNodeSystem_oom_recent_is_critical_and_names_window(t *testing.T) {
	var got *Alert
	for _, a := range oomAlerts(&report.SystemReport{OOMKills: 2}) {
		if strings.Contains(a.Message, "OOM") {
			a := a
			got = &a
		}
	}
	if got == nil || got.Severity != AlertCritical || got.Message != "2 OOM kills in the last hour" {
		t.Fatalf("alert = %+v", got)
	}
}

func TestCheckNodeSystem_oom_none_is_silent(t *testing.T) {
	for _, a := range oomAlerts(&report.SystemReport{}) {
		if strings.Contains(a.Message, "OOM") {
			t.Fatalf("unexpected alert %+v", a)
		}
	}
}

func TestCheckNodeSystem_oom_unknown_warns_not_zero(t *testing.T) {
	var got *Alert
	for _, a := range oomAlerts(&report.SystemReport{OOMKillsError: "journalctl: exit status 1"}) {
		if strings.Contains(a.Message, "OOM") {
			a := a
			got = &a
		}
	}
	if got == nil || got.Severity != AlertWarning || !strings.Contains(got.Message, "unknown") {
		t.Fatalf("alert = %+v", got)
	}
}
