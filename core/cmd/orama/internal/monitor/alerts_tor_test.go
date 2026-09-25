package monitor

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/report"
)

func TestCheckNodeTor_nil(t *testing.T) {
	if alerts := checkNodeTor(&report.NodeReport{}, "10.0.0.1"); len(alerts) != 0 {
		t.Errorf("expected no alerts without a Tor report, got %v", alerts)
	}
}

func TestCheckNodeTor_healthy(t *testing.T) {
	r := &report.NodeReport{Tor: &report.TorReport{
		ClientActive: true, SocksListening: true, Bootstrapped: true, BootstrapPct: 100,
	}}
	if alerts := checkNodeTor(r, "10.0.0.1"); len(alerts) != 0 {
		t.Errorf("a healthy Tor client must not alert, got %v", alerts)
	}
}

func TestCheckNodeTor_socksDownAndBootstrapping(t *testing.T) {
	r := &report.NodeReport{Tor: &report.TorReport{ClientActive: true, BootstrapPct: 45}}
	alerts := checkNodeTor(r, "10.0.0.1")
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts (socks, bootstrap), got %v", alerts)
	}
	for _, a := range alerts {
		if a.Subsystem != "tor" || a.Severity != AlertWarning {
			t.Errorf("unexpected alert %+v", a)
		}
	}
}

// An unknown bootstrap (journal vacuumed) is not evidence of a problem.
func TestCheckNodeTor_unknownBootstrapDoesNotAlert(t *testing.T) {
	r := &report.NodeReport{Tor: &report.TorReport{ClientActive: true, SocksListening: true, BootstrapPct: -1}}
	if alerts := checkNodeTor(r, "10.0.0.1"); len(alerts) != 0 {
		t.Errorf("unknown bootstrap must not alert, got %v", alerts)
	}
}

func TestCheckNodeTor_legacyAnyone(t *testing.T) {
	r := &report.NodeReport{Tor: &report.TorReport{
		ClientActive: true, SocksListening: true, Bootstrapped: true, BootstrapPct: 100, LegacyAnyone: true,
	}}
	alerts := checkNodeTor(r, "10.0.0.1")
	if len(alerts) != 1 || !strings.Contains(alerts[0].Message, "Anyone") {
		t.Errorf("expected one Anyone-leftover alert, got %v", alerts)
	}
}
