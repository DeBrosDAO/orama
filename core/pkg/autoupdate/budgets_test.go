package autoupdate

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestBudgets_anInstallFitsInsideTheLockAndTheRun(t *testing.T) {
	if installBudget >= LockTTL {
		t.Errorf("an install may take %s, and the rollout lock's lease is %s: another node could take the lock mid-install", installBudget, LockTTL)
	}
	if fetchBudget+installBudget > RunTimeout {
		t.Errorf("a run may take %s and the service is killed after %s", fetchBudget+installBudget, RunTimeout)
	}
}

func TestBudgets_theUnitIsKilledAfterTheRunTimeout(t *testing.T) {
	unit, err := os.ReadFile("../../systemd/orama-autoupdate.service")
	if err != nil {
		t.Fatal(err)
	}
	want := "TimeoutStartSec=" + RunTimeout.String()
	if RunTimeout == time.Hour {
		want = "TimeoutStartSec=1h"
	}
	if !strings.Contains(string(unit), want+"\n") {
		t.Fatalf("orama-autoupdate.service does not say %s", want)
	}
}
