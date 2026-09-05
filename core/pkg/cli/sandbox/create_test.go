package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

func TestFindProjectRoot_FromSubDir(t *testing.T) {
	// Create a temp dir with go.mod (resolve symlinks for macOS /private/var)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a nested subdir
	sub := filepath.Join(root, "pkg", "foo")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	// Change to subdir and find root
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(sub)

	got, err := findProjectRoot()
	if err != nil {
		t.Fatalf("findProjectRoot() error: %v", err)
	}
	if got != root {
		t.Errorf("findProjectRoot() = %q, want %q", got, root)
	}
}

func TestFindProjectRoot_NoGoMod(t *testing.T) {
	// Create a temp dir without go.mod
	dir := t.TempDir()

	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	os.Chdir(dir)

	_, err := findProjectRoot()
	if err == nil {
		t.Error("findProjectRoot() should error when no go.mod exists")
	}
}

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

func TestIsHex(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"abcdef0123456789", true},
		{"ABCDEF", true},
		{"0", true},
		{"", true}, // vacuous truth, but guarded by len check in caller
		{"xyz", false},
		{"abcg", false},
		{"abc def", false},
	}
	for _, tt := range tests {
		got := isHex(tt.input)
		if got != tt.want {
			t.Errorf("isHex(%q) = %v, want %v", tt.input, got, tt.want)
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
