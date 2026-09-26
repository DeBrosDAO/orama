package sandbox

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

func TestIsSafeDNSName(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"example.com", true},
		{"test-cluster.orama.network", true},
		{"a", true},
		{"", false},
		{"test;rm -rf /", false},
		{"test$(whoami)", false},
		{"test space", false},
		{"test_underscore", false},
		{"UPPER.case.OK", true},
		{"123.456", true},
	}
	for _, tt := range tests {
		got := isSafeDNSName(tt.input)
		if got != tt.want {
			t.Errorf("isSafeDNSName(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestValidateAgentStatus_Locked(t *testing.T) {
	status := &rwagent.StatusResponse{Locked: true, ConnectedApps: 1}
	err := validateAgentStatus(status)
	if err == nil {
		t.Fatal("expected error for locked agent")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("error should mention locked, got: %v", err)
	}
}

// A prompt already on screen is the difference between "unlock it" and "you
// have one waiting". The agent reports the count; this client used to drop it,
// so someone with an unanswered prompt was told to go and unlock a wallet that
// was already asking them to.
func TestValidateAgentStatus_LockedWithPendingPrompt(t *testing.T) {
	status := &rwagent.StatusResponse{Locked: true, ConnectedApps: 1, PendingUnlocks: 2}
	err := validateAgentStatus(status)
	if err == nil {
		t.Fatal("expected error for locked agent")
	}
	if !strings.Contains(err.Error(), "2 approval prompt") {
		t.Errorf("error should say how many prompts are waiting, got: %v", err)
	}
	if !strings.Contains(err.Error(), "waiting") {
		t.Errorf("error should say the prompts are waiting to be answered, got: %v", err)
	}
}

func TestValidateAgentStatus_LockedWithNoPendingPrompt(t *testing.T) {
	status := &rwagent.StatusResponse{Locked: true, ConnectedApps: 1, PendingUnlocks: 0}
	err := validateAgentStatus(status)
	if err == nil {
		t.Fatal("expected error for locked agent")
	}
	if strings.Contains(err.Error(), "approval prompt") {
		t.Errorf("no prompt is waiting, so none should be mentioned: %v", err)
	}
	if !strings.Contains(err.Error(), "Unlock it") {
		t.Errorf("error should say to unlock it, got: %v", err)
	}
}

func TestValidateAgentStatus_NoDesktopApp(t *testing.T) {
	status := &rwagent.StatusResponse{Locked: false, ConnectedApps: 0}
	err := validateAgentStatus(status)
	if err == nil {
		t.Fatal("expected error when no desktop app connected")
	}
	if !strings.Contains(err.Error(), "desktop app") {
		t.Errorf("error should mention desktop app, got: %v", err)
	}
}

func TestValidateAgentStatus_Ready(t *testing.T) {
	status := &rwagent.StatusResponse{Locked: false, ConnectedApps: 1}
	if err := validateAgentStatus(status); err != nil {
		t.Errorf("expected no error for ready agent, got: %v", err)
	}
}
