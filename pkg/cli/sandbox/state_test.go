package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadState(t *testing.T) {
	// Use temp dir for test
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	state := &SandboxState{
		Name:      "test-sandbox",
		CreatedAt: time.Date(2026, 2, 25, 10, 0, 0, 0, time.UTC),
		Domain:    "test.example.com",
		Status:    StatusRunning,
		Servers: []ServerState{
			{ID: 1, Name: "sbx-test-1", IP: "1.1.1.1", Role: "nameserver", FloatingIP: "10.0.0.1", WgIP: "10.0.0.1"},
			{ID: 2, Name: "sbx-test-2", IP: "2.2.2.2", Role: "nameserver", FloatingIP: "10.0.0.2", WgIP: "10.0.0.2"},
			{ID: 3, Name: "sbx-test-3", IP: "3.3.3.3", Role: "node", WgIP: "10.0.0.3"},
			{ID: 4, Name: "sbx-test-4", IP: "4.4.4.4", Role: "node", WgIP: "10.0.0.4"},
			{ID: 5, Name: "sbx-test-5", IP: "5.5.5.5", Role: "node", WgIP: "10.0.0.5"},
		},
	}

	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	// Verify file exists
	expected := filepath.Join(tmpDir, ".orama", "sandboxes", "test-sandbox.yaml")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("state file not created at %s: %v", expected, err)
	}

	// Load back
	loaded, err := LoadState("test-sandbox")
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}

	if loaded.Name != "test-sandbox" {
		t.Errorf("name = %s, want test-sandbox", loaded.Name)
	}
	if loaded.Domain != "test.example.com" {
		t.Errorf("domain = %s, want test.example.com", loaded.Domain)
	}
	if loaded.Status != StatusRunning {
		t.Errorf("status = %s, want running", loaded.Status)
	}
	if len(loaded.Servers) != 5 {
		t.Errorf("servers = %d, want 5", len(loaded.Servers))
	}
}

func TestLoadState_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	_, err := LoadState("nonexistent")
	if err == nil {
		t.Error("LoadState() expected error for nonexistent sandbox")
	}
}

func TestDeleteState(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	state := &SandboxState{
		Name:   "to-delete",
		Status: StatusRunning,
	}
	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	if err := DeleteState("to-delete"); err != nil {
		t.Fatalf("DeleteState() error = %v", err)
	}

	_, err := LoadState("to-delete")
	if err == nil {
		t.Error("LoadState() should fail after DeleteState()")
	}
}

func TestListStates(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// Create 2 sandboxes
	for _, name := range []string{"sandbox-a", "sandbox-b"} {
		if err := SaveState(&SandboxState{Name: name, Status: StatusRunning}); err != nil {
			t.Fatalf("SaveState(%s) error = %v", name, err)
		}
	}

	states, err := ListStates()
	if err != nil {
		t.Fatalf("ListStates() error = %v", err)
	}
	if len(states) != 2 {
		t.Errorf("ListStates() returned %d, want 2", len(states))
	}
}

func TestFindActiveSandbox(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	// No sandboxes
	active, err := FindActiveSandbox()
	if err != nil {
		t.Fatalf("FindActiveSandbox() error = %v", err)
	}
	if active != nil {
		t.Error("expected nil when no sandboxes exist")
	}

	// Add one running sandbox
	if err := SaveState(&SandboxState{Name: "active-one", Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(&SandboxState{Name: "errored-one", Status: StatusError}); err != nil {
		t.Fatal(err)
	}

	active, err = FindActiveSandbox()
	if err != nil {
		t.Fatalf("FindActiveSandbox() error = %v", err)
	}
	if active == nil || active.Name != "active-one" {
		t.Errorf("FindActiveSandbox() = %v, want active-one", active)
	}
}

func TestToNodes(t *testing.T) {
	state := &SandboxState{
		Servers: []ServerState{
			{IP: "1.1.1.1", Role: "nameserver"},
			{IP: "2.2.2.2", Role: "node"},
		},
	}

	nodes := state.ToNodes("/tmp/key")
	if len(nodes) != 2 {
		t.Fatalf("ToNodes() returned %d nodes, want 2", len(nodes))
	}
	if nodes[0].Host != "1.1.1.1" {
		t.Errorf("node[0].Host = %s, want 1.1.1.1", nodes[0].Host)
	}
	if nodes[0].User != "root" {
		t.Errorf("node[0].User = %s, want root", nodes[0].User)
	}
	if nodes[0].SSHKey != "/tmp/key" {
		t.Errorf("node[0].SSHKey = %s, want /tmp/key", nodes[0].SSHKey)
	}
	if nodes[0].Environment != "sandbox" {
		t.Errorf("node[0].Environment = %s, want sandbox", nodes[0].Environment)
	}
}

func TestNameserverAndRegularNodes(t *testing.T) {
	state := &SandboxState{
		Servers: []ServerState{
			{Role: "nameserver"},
			{Role: "nameserver"},
			{Role: "node"},
			{Role: "node"},
			{Role: "node"},
		},
	}

	ns := state.NameserverNodes()
	if len(ns) != 2 {
		t.Errorf("NameserverNodes() = %d, want 2", len(ns))
	}

	regular := state.RegularNodes()
	if len(regular) != 3 {
		t.Errorf("RegularNodes() = %d, want 3", len(regular))
	}
}

func TestGenesisServer(t *testing.T) {
	state := &SandboxState{
		Servers: []ServerState{
			{Name: "first"},
			{Name: "second"},
		},
	}
	if state.GenesisServer().Name != "first" {
		t.Errorf("GenesisServer().Name = %s, want first", state.GenesisServer().Name)
	}

	empty := &SandboxState{}
	if empty.GenesisServer().Name != "" {
		t.Error("GenesisServer() on empty state should return zero value")
	}
}
