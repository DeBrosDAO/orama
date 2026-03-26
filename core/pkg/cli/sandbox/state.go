package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"gopkg.in/yaml.v3"
)

// SandboxStatus represents the lifecycle state of a sandbox.
type SandboxStatus string

const (
	StatusCreating   SandboxStatus = "creating"
	StatusRunning    SandboxStatus = "running"
	StatusDestroying SandboxStatus = "destroying"
	StatusError      SandboxStatus = "error"
)

// SandboxState holds the full state of an active sandbox cluster.
type SandboxState struct {
	Name      string         `yaml:"name"`
	CreatedAt time.Time      `yaml:"created_at"`
	Domain    string         `yaml:"domain"`
	Status    SandboxStatus  `yaml:"status"`
	Servers   []ServerState  `yaml:"servers"`
}

// ServerState holds the state of a single server in the sandbox.
type ServerState struct {
	ID         int64  `yaml:"id"`                    // Hetzner server ID
	Name       string `yaml:"name"`                  // e.g., sbx-feature-webrtc-1
	IP         string `yaml:"ip"`                    // Public IPv4
	Role       string `yaml:"role"`                  // "nameserver" or "node"
	FloatingIP string `yaml:"floating_ip,omitempty"` // Only for nameserver nodes
	WgIP       string `yaml:"wg_ip,omitempty"`       // WireGuard IP (populated after install)
}

// sandboxesDir returns ~/.orama/sandboxes/, creating it if needed.
func sandboxesDir() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	sbxDir := filepath.Join(dir, "sandboxes")
	if err := os.MkdirAll(sbxDir, 0700); err != nil {
		return "", fmt.Errorf("create sandboxes directory: %w", err)
	}
	return sbxDir, nil
}

// statePath returns the path for a sandbox's state file.
func statePath(name string) (string, error) {
	dir, err := sandboxesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".yaml"), nil
}

// SaveState persists the sandbox state to disk.
func SaveState(state *SandboxState) error {
	path, err := statePath(state.Name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}

	return nil
}

// LoadState reads a sandbox state from disk.
func LoadState(name string) (*SandboxState, error) {
	path, err := statePath(name)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("sandbox %q not found", name)
		}
		return nil, fmt.Errorf("read state: %w", err)
	}

	var state SandboxState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}

	return &state, nil
}

// DeleteState removes the sandbox state file.
func DeleteState(name string) error {
	path, err := statePath(name)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete state: %w", err)
	}

	return nil
}

// ListStates returns all sandbox states from disk.
func ListStates() ([]*SandboxState, error) {
	dir, err := sandboxesDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read sandboxes directory: %w", err)
	}

	var states []*SandboxState
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".yaml")
		state, err := LoadState(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load sandbox %q: %v\n", name, err)
			continue
		}
		states = append(states, state)
	}

	return states, nil
}

// FindActiveSandbox returns the first sandbox in running or creating state.
// Returns nil if no active sandbox exists.
func FindActiveSandbox() (*SandboxState, error) {
	states, err := ListStates()
	if err != nil {
		return nil, err
	}

	for _, s := range states {
		if s.Status == StatusRunning || s.Status == StatusCreating {
			return s, nil
		}
	}

	return nil, nil
}

// ToNodes converts sandbox servers to inspector.Node structs for SSH operations.
// Sets VaultTarget on each node so PrepareNodeKeys resolves from the wallet.
func (s *SandboxState) ToNodes(vaultTarget string) []inspector.Node {
	nodes := make([]inspector.Node, len(s.Servers))
	for i, srv := range s.Servers {
		nodes[i] = inspector.Node{
			Environment: "sandbox",
			User:        "root",
			Host:        srv.IP,
			Role:        srv.Role,
			VaultTarget: vaultTarget,
		}
	}
	return nodes
}

// NameserverNodes returns only the nameserver nodes.
func (s *SandboxState) NameserverNodes() []ServerState {
	var ns []ServerState
	for _, srv := range s.Servers {
		if srv.Role == "nameserver" {
			ns = append(ns, srv)
		}
	}
	return ns
}

// RegularNodes returns only the non-nameserver nodes.
func (s *SandboxState) RegularNodes() []ServerState {
	var nodes []ServerState
	for _, srv := range s.Servers {
		if srv.Role == "node" {
			nodes = append(nodes, srv)
		}
	}
	return nodes
}

// GenesisServer returns the first server (genesis node).
func (s *SandboxState) GenesisServer() ServerState {
	if len(s.Servers) == 0 {
		return ServerState{}
	}
	return s.Servers[0]
}
