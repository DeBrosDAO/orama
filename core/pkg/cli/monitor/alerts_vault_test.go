package monitor

import (
	"testing"

	"github.com/DeBrosOfficial/network/pkg/cli/production/report"
)

func TestCheckNodeVault_nil(t *testing.T) {
	r := &report.NodeReport{}
	alerts := checkNodeVault(r, "10.0.0.1")
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts for nil vault, got %d", len(alerts))
	}
}

func TestCheckNodeVault_serviceInactive(t *testing.T) {
	r := &report.NodeReport{
		Vault: &report.VaultReport{ServiceActive: false},
	}
	alerts := checkNodeVault(r, "10.0.0.1")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != AlertCritical {
		t.Errorf("expected critical, got %s", alerts[0].Severity)
	}
}

func TestCheckNodeVault_unresponsive(t *testing.T) {
	r := &report.NodeReport{
		Vault: &report.VaultReport{ServiceActive: true, Responsive: false},
	}
	alerts := checkNodeVault(r, "10.0.0.1")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != AlertWarning {
		t.Errorf("expected warning, got %s", alerts[0].Severity)
	}
}

func TestCheckNodeVault_unavailable(t *testing.T) {
	r := &report.NodeReport{
		Vault: &report.VaultReport{
			ServiceActive: true,
			Responsive:    true,
			Status:        "unavailable",
			Guardians:     5,
			Healthy:       1,
			Threshold:     3,
			WriteQuorum:   4,
		},
	}
	alerts := checkNodeVault(r, "10.0.0.1")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != AlertCritical {
		t.Errorf("expected critical, got %s", alerts[0].Severity)
	}
}

func TestCheckNodeVault_degraded(t *testing.T) {
	r := &report.NodeReport{
		Vault: &report.VaultReport{
			ServiceActive: true,
			Responsive:    true,
			Status:        "degraded",
			Guardians:     5,
			Healthy:       3,
			Threshold:     3,
			WriteQuorum:   4,
		},
	}
	alerts := checkNodeVault(r, "10.0.0.1")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != AlertWarning {
		t.Errorf("expected warning, got %s", alerts[0].Severity)
	}
}

func TestCheckNodeVault_excessiveRestarts(t *testing.T) {
	r := &report.NodeReport{
		Vault: &report.VaultReport{
			ServiceActive: true,
			Responsive:    true,
			Status:        "healthy",
			RestartCount:  5,
		},
	}
	alerts := checkNodeVault(r, "10.0.0.1")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Severity != AlertWarning {
		t.Errorf("expected warning, got %s", alerts[0].Severity)
	}
}

func TestCheckNodeVault_healthy(t *testing.T) {
	r := &report.NodeReport{
		Vault: &report.VaultReport{
			ServiceActive: true,
			Responsive:    true,
			Status:        "healthy",
			Guardians:     5,
			Healthy:       5,
			Threshold:     3,
			WriteQuorum:   4,
			RestartCount:  0,
		},
	}
	alerts := checkNodeVault(r, "10.0.0.1")
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts for healthy vault, got %d", len(alerts))
	}
}
