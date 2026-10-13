package cluster

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/hardening"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func hardenedLive() *hardening.Live {
	l := &hardening.Live{Sysctls: map[string]string{}, ApportLoadState: "not-found"}
	for _, s := range hardening.Sysctls {
		l.Sysctls[s.Key] = s.Want
	}
	return l
}

func hardeningAlertsOf(l *hardening.Live) []Alert {
	var out []Alert
	for _, a := range checkNodeSystem(&report.NodeReport{System: &report.SystemReport{Hardening: l}}, "node-7") {
		if strings.Contains(a.Message, "hardening") {
			out = append(out, a)
		}
	}
	return out
}

func TestCheckNodeSystem_hardened_node_is_silent(t *testing.T) {
	if got := hardeningAlertsOf(hardenedLive()); len(got) != 0 {
		t.Fatalf("alerts on a hardened node: %+v", got)
	}
}

func TestCheckNodeSystem_hardening_not_reported_is_silent(t *testing.T) {
	if got := hardeningAlertsOf(nil); len(got) != 0 {
		t.Fatalf("alerts from a release that does not report it: %+v", got)
	}
}

func TestCheckNodeSystem_hardening_drift_warns_and_names_node_and_setting(t *testing.T) {
	l := hardenedLive()
	l.Sysctls["fs.suid_dumpable"] = "2"
	l.SwapDevices = 1
	got := hardeningAlertsOf(l)
	if len(got) != 2 {
		t.Fatalf("alerts = %+v, want one per drifted setting", got)
	}
	for _, a := range got {
		if a.Severity != AlertWarning || a.Node != "node-7" {
			t.Errorf("alert = %+v, want a warning for node-7", a)
		}
	}
	if !strings.Contains(got[0].Message, "fs.suid_dumpable") || !strings.Contains(got[1].Message, "swap") {
		t.Errorf("messages %q / %q do not name the drifted settings", got[0].Message, got[1].Message)
	}
}

func TestCheckNodeSystem_hardening_unreadable_setting_warns(t *testing.T) {
	l := hardenedLive()
	l.Errors = []string{"cannot read fs.suid_dumpable: permission denied"}
	if got := hardeningAlertsOf(l); len(got) != 1 || got[0].Severity != AlertWarning {
		t.Fatalf("alerts = %+v, want one warning", got)
	}
}
