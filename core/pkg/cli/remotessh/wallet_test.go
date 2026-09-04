package remotessh

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

const testPrivateKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nfake-key-data\n-----END OPENSSH PRIVATE KEY-----"

// mockClient implements vaultClient for testing.
type mockClient struct {
	getSSHKey      func(ctx context.Context, host, username, format string) (*rwagent.VaultSSHData, error)
	createSSHEntry func(ctx context.Context, host, username string) (*rwagent.VaultSSHData, error)
}

func (m *mockClient) GetSSHKey(ctx context.Context, host, username, format string) (*rwagent.VaultSSHData, error) {
	return m.getSSHKey(ctx, host, username, format)
}

func (m *mockClient) CreateSSHEntry(ctx context.Context, host, username string) (*rwagent.VaultSSHData, error) {
	return m.createSSHEntry(ctx, host, username)
}

// withMockClient replaces newClient for the duration of a test.
func withMockClient(t *testing.T, mock *mockClient) {
	t.Helper()
	orig := newClient
	newClient = func() vaultClient { return mock }
	t.Cleanup(func() { newClient = orig })
}

func TestParseVaultTarget(t *testing.T) {
	tests := []struct {
		target   string
		wantHost string
		wantUser string
	}{
		{"sandbox/root", "sandbox", "root"},
		{"192.168.1.1/ubuntu", "192.168.1.1", "ubuntu"},
		{"my-host/my-user", "my-host", "my-user"},
		{"noslash", "noslash", ""},
		{"a/b/c", "a", "b/c"},
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			host, user := parseVaultTarget(tt.target)
			if host != tt.wantHost {
				t.Errorf("parseVaultTarget(%q) host = %q, want %q", tt.target, host, tt.wantHost)
			}
			if user != tt.wantUser {
				t.Errorf("parseVaultTarget(%q) user = %q, want %q", tt.target, user, tt.wantUser)
			}
		})
	}
}

func TestWrapAgentError_notRunning(t *testing.T) {
	err := wrapAgentError(rwagent.ErrAgentNotRunning, "test action")
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected 'not running' message, got: %s", err)
	}
	if !strings.Contains(err.Error(), "rw agent start") {
		t.Errorf("expected actionable hint, got: %s", err)
	}
}

func TestWrapAgentError_locked(t *testing.T) {
	agentErr := &rwagent.AgentError{Code: "AGENT_LOCKED", Message: "agent is locked"}
	err := wrapAgentError(agentErr, "test action")
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("expected 'locked' message, got: %s", err)
	}
	if !strings.Contains(err.Error(), "rw agent unlock") {
		t.Errorf("expected actionable hint, got: %s", err)
	}
}

func TestWrapAgentError_generic(t *testing.T) {
	err := wrapAgentError(errors.New("some error"), "test action")
	if !strings.Contains(err.Error(), "test action") {
		t.Errorf("expected action context, got: %s", err)
	}
	if !strings.Contains(err.Error(), "some error") {
		t.Errorf("expected wrapped error, got: %s", err)
	}
}

func TestPrepareNodeKeys_success(t *testing.T) {
	mock := &mockClient{
		getSSHKey: func(_ context.Context, host, username, format string) (*rwagent.VaultSSHData, error) {
			return &rwagent.VaultSSHData{PrivateKey: testPrivateKey}, nil
		},
	}
	withMockClient(t, mock)

	nodes := []inspector.Node{
		{Host: "10.0.0.1", User: "root"},
		{Host: "10.0.0.2", User: "root"},
	}

	cleanup, err := PrepareNodeKeys(nodes)
	if err != nil {
		t.Fatalf("PrepareNodeKeys() error = %v", err)
	}
	defer cleanup()

	for i, n := range nodes {
		if n.SSHKey == "" {
			t.Errorf("node[%d].SSHKey is empty", i)
			continue
		}
		data, err := os.ReadFile(n.SSHKey)
		if err != nil {
			t.Errorf("node[%d] key file unreadable: %v", i, err)
			continue
		}
		if !strings.Contains(string(data), "BEGIN OPENSSH PRIVATE KEY") {
			t.Errorf("node[%d] key file has wrong content", i)
		}
	}
}

func TestPrepareNodeKeys_deduplication(t *testing.T) {
	callCount := 0
	mock := &mockClient{
		getSSHKey: func(_ context.Context, host, username, format string) (*rwagent.VaultSSHData, error) {
			callCount++
			return &rwagent.VaultSSHData{PrivateKey: testPrivateKey}, nil
		},
	}
	withMockClient(t, mock)

	nodes := []inspector.Node{
		{Host: "10.0.0.1", User: "root"},
		{Host: "10.0.0.1", User: "root"}, // same host/user
	}

	cleanup, err := PrepareNodeKeys(nodes)
	if err != nil {
		t.Fatalf("PrepareNodeKeys() error = %v", err)
	}
	defer cleanup()

	if callCount != 1 {
		t.Errorf("expected 1 agent call (dedup), got %d", callCount)
	}
	if nodes[0].SSHKey != nodes[1].SSHKey {
		t.Error("expected same key path for deduplicated nodes")
	}
}

func TestPrepareNodeKeys_vaultTarget(t *testing.T) {
	var capturedHost, capturedUser string
	mock := &mockClient{
		getSSHKey: func(_ context.Context, host, username, format string) (*rwagent.VaultSSHData, error) {
			capturedHost = host
			capturedUser = username
			return &rwagent.VaultSSHData{PrivateKey: testPrivateKey}, nil
		},
	}
	withMockClient(t, mock)

	nodes := []inspector.Node{
		{Host: "10.0.0.1", User: "root", VaultTarget: "sandbox/admin"},
	}

	cleanup, err := PrepareNodeKeys(nodes)
	if err != nil {
		t.Fatalf("PrepareNodeKeys() error = %v", err)
	}
	defer cleanup()

	if capturedHost != "sandbox" || capturedUser != "admin" {
		t.Errorf("expected host=sandbox user=admin, got host=%s user=%s", capturedHost, capturedUser)
	}
}

func TestPrepareNodeKeys_agentNotRunning(t *testing.T) {
	mock := &mockClient{
		getSSHKey: func(_ context.Context, _, _, _ string) (*rwagent.VaultSSHData, error) {
			return nil, rwagent.ErrAgentNotRunning
		},
	}
	withMockClient(t, mock)

	nodes := []inspector.Node{{Host: "10.0.0.1", User: "root"}}
	_, err := PrepareNodeKeys(nodes)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected 'not running' error, got: %s", err)
	}
}

func TestPrepareNodeKeys_invalidKey(t *testing.T) {
	mock := &mockClient{
		getSSHKey: func(_ context.Context, _, _, _ string) (*rwagent.VaultSSHData, error) {
			return &rwagent.VaultSSHData{PrivateKey: "garbage"}, nil
		},
	}
	withMockClient(t, mock)

	nodes := []inspector.Node{{Host: "10.0.0.1", User: "root"}}
	_, err := PrepareNodeKeys(nodes)
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
	if !strings.Contains(err.Error(), "invalid key") {
		t.Errorf("expected 'invalid key' error, got: %s", err)
	}
}

func TestPrepareNodeKeys_cleanupOnError(t *testing.T) {
	callNum := 0
	mock := &mockClient{
		getSSHKey: func(_ context.Context, _, _, _ string) (*rwagent.VaultSSHData, error) {
			callNum++
			if callNum == 2 {
				return nil, &rwagent.AgentError{Code: "AGENT_LOCKED", Message: "locked"}
			}
			return &rwagent.VaultSSHData{PrivateKey: testPrivateKey}, nil
		},
	}
	withMockClient(t, mock)

	nodes := []inspector.Node{
		{Host: "10.0.0.1", User: "root"},
		{Host: "10.0.0.2", User: "root"},
	}

	_, err := PrepareNodeKeys(nodes)
	if err == nil {
		t.Fatal("expected error")
	}

	// First node's temp file should have been cleaned up
	if nodes[0].SSHKey != "" {
		if _, statErr := os.Stat(nodes[0].SSHKey); statErr == nil {
			t.Error("expected temp key file to be cleaned up on error")
		}
	}
}

func TestPrepareNodeKeys_emptyNodes(t *testing.T) {
	mock := &mockClient{}
	withMockClient(t, mock)

	cleanup, err := PrepareNodeKeys(nil)
	if err != nil {
		t.Fatalf("expected no error for empty nodes, got: %v", err)
	}
	cleanup() // should not panic
}

func TestEnsureVaultEntry_exists(t *testing.T) {
	mock := &mockClient{
		getSSHKey: func(_ context.Context, _, _, _ string) (*rwagent.VaultSSHData, error) {
			return &rwagent.VaultSSHData{PublicKey: "ssh-ed25519 AAAA..."}, nil
		},
	}
	withMockClient(t, mock)

	if err := EnsureVaultEntry("sandbox/root"); err != nil {
		t.Fatalf("EnsureVaultEntry() error = %v", err)
	}
}

func TestEnsureVaultEntry_creates(t *testing.T) {
	created := false
	mock := &mockClient{
		getSSHKey: func(_ context.Context, _, _, _ string) (*rwagent.VaultSSHData, error) {
			return nil, &rwagent.AgentError{Code: "NOT_FOUND", Message: "not found"}
		},
		createSSHEntry: func(_ context.Context, host, username string) (*rwagent.VaultSSHData, error) {
			created = true
			if host != "sandbox" || username != "root" {
				t.Errorf("unexpected create args: %s/%s", host, username)
			}
			return &rwagent.VaultSSHData{PublicKey: "ssh-ed25519 AAAA..."}, nil
		},
	}
	withMockClient(t, mock)

	if err := EnsureVaultEntry("sandbox/root"); err != nil {
		t.Fatalf("EnsureVaultEntry() error = %v", err)
	}
	if !created {
		t.Error("expected CreateSSHEntry to be called")
	}
}

func TestEnsureVaultEntry_locked(t *testing.T) {
	mock := &mockClient{
		getSSHKey: func(_ context.Context, _, _, _ string) (*rwagent.VaultSSHData, error) {
			return nil, &rwagent.AgentError{Code: "AGENT_LOCKED", Message: "locked"}
		},
	}
	withMockClient(t, mock)

	err := EnsureVaultEntry("sandbox/root")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("expected locked error, got: %s", err)
	}
}

func TestResolveVaultPublicKey_success(t *testing.T) {
	mock := &mockClient{
		getSSHKey: func(_ context.Context, _, _, format string) (*rwagent.VaultSSHData, error) {
			if format != "pub" {
				t.Errorf("expected format=pub, got %s", format)
			}
			return &rwagent.VaultSSHData{PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA..."}, nil
		},
	}
	withMockClient(t, mock)

	key, err := ResolveVaultPublicKey("sandbox/root")
	if err != nil {
		t.Fatalf("ResolveVaultPublicKey() error = %v", err)
	}
	if !strings.HasPrefix(key, "ssh-") {
		t.Errorf("expected ssh- prefix, got: %s", key)
	}
}

func TestResolveVaultPublicKey_invalidFormat(t *testing.T) {
	mock := &mockClient{
		getSSHKey: func(_ context.Context, _, _, _ string) (*rwagent.VaultSSHData, error) {
			return &rwagent.VaultSSHData{PublicKey: "not-a-valid-key"}, nil
		},
	}
	withMockClient(t, mock)

	_, err := ResolveVaultPublicKey("sandbox/root")
	if err == nil {
		t.Fatal("expected error for invalid public key")
	}
	if !strings.Contains(err.Error(), "invalid public key") {
		t.Errorf("expected 'invalid public key' error, got: %s", err)
	}
}

func TestResolveVaultPublicKey_notFound(t *testing.T) {
	mock := &mockClient{
		getSSHKey: func(_ context.Context, _, _, _ string) (*rwagent.VaultSSHData, error) {
			return nil, &rwagent.AgentError{Code: "NOT_FOUND", Message: "not found"}
		},
	}
	withMockClient(t, mock)

	_, err := ResolveVaultPublicKey("sandbox/root")
	if err == nil {
		t.Fatal("expected error")
	}
}

// Wallet-derived node keys are handed to ssh only as ephemeral 0600 files that
// PrepareNodeKeys zero-overwrites on cleanup. They must never be added to the
// operator's ssh-agent: an agent holds a key until it is explicitly removed or
// the agent dies, which outlives the deploy and defeats keeping the key in a
// lockable vault. Push fanout stages each target's key on the hub and
// authenticates with "-i <key> -o IdentitiesOnly=yes", so agent forwarding is
// not needed by anything.
//
// This reads the package source because the property being protected is the
// absence of code — a behavioural test cannot observe a call that is not there.
func TestPackageNeverLoadsKeysIntoSSHAgent(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	forbidden := map[string]string{
		"ssh-add": "adds a wallet-derived key to the operator's ssh-agent, where it outlives the deploy",
		`"-A"`:    "enables ssh agent forwarding, which exposes every loaded key to the remote host",
	}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for needle, why := range forbidden {
			if strings.Contains(string(src), needle) {
				t.Errorf("%s contains %s, which %s", name, needle, why)
			}
		}
	}
}
