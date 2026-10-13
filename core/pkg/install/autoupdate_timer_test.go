package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
)

// Phase 5 enables the timer for the next boot and does not start it: started
// before orama-node, it was due at once and its agent read the settings from an
// rqlite that was not up yet.
func TestEnableAutoUpdate_enablesWithoutStarting(t *testing.T) {
	log := fakeSystemctl(t)
	ps := &ProductionSetup{serviceController: NewSystemdController()}
	if err := ps.enableAutoUpdate(); err != nil {
		t.Fatal(err)
	}
	want := []string{"enable " + systemd.AutoUpdateTimerName}
	if got := calls(t, log); !slices.Equal(got, want) {
		t.Fatalf("systemctl calls %v, want %v", got, want)
	}
}

// StartAutoUpdate restarts the timer, so an upgrade's new unit takes effect.
func TestStartAutoUpdate_restartsTheTimer(t *testing.T) {
	log := fakeSystemctl(t)
	ps := &ProductionSetup{serviceController: NewSystemdController()}
	if err := ps.StartAutoUpdate(); err != nil {
		t.Fatal(err)
	}
	want := []string{"restart " + systemd.AutoUpdateTimerName}
	if got := calls(t, log); !slices.Equal(got, want) {
		t.Fatalf("systemctl calls %v, want %v", got, want)
	}
}

func TestStartAutoUpdate_aFailedStartIsAnError(t *testing.T) {
	failingSystemctl(t)
	ps := &ProductionSetup{serviceController: NewSystemdController()}
	if err := ps.StartAutoUpdate(); err == nil {
		t.Fatal("a timer that could not be started was reported as started")
	}
}

func TestEnableAutoUpdate_aFailedEnableIsAnError(t *testing.T) {
	failingSystemctl(t)
	ps := &ProductionSetup{serviceController: NewSystemdController()}
	if err := ps.enableAutoUpdate(); err == nil {
		t.Fatal("a timer that could not be enabled was reported as enabled")
	}
}

// failingSystemctl puts a systemctl that fails every call first on PATH.
func failingSystemctl(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
