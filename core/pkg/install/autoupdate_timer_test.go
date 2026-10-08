package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
)

// The timer is enabled for the next boot and started now, so a node that was
// installed or upgraded begins looking for a release without a reboot.
func TestEnableAutoUpdate_enablesAndStartsTheTimer(t *testing.T) {
	log := fakeSystemctl(t)
	ps := &ProductionSetup{serviceController: NewSystemdController()}
	if err := ps.enableAutoUpdate(); err != nil {
		t.Fatal(err)
	}
	want := []string{"enable " + systemd.AutoUpdateTimerName, "restart " + systemd.AutoUpdateTimerName}
	if got := calls(t, log); !slices.Equal(got, want) {
		t.Fatalf("systemctl calls %v, want %v", got, want)
	}
}

func TestEnableAutoUpdate_aFailedEnableIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ps := &ProductionSetup{serviceController: NewSystemdController()}
	if err := ps.enableAutoUpdate(); err == nil {
		t.Fatal("a timer that could not be enabled was reported as enabled")
	}
}
