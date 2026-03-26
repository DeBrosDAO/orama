package rwagent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// startMockAgent creates a mock agent server on a Unix socket for testing.
func startMockAgent(t *testing.T, handler http.Handler) (socketPath string, cleanup func()) {
	t.Helper()

	tmpDir := t.TempDir()
	socketPath = filepath.Join(tmpDir, "test-agent.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()

	cleanup = func() {
		_ = server.Close()
		_ = os.Remove(socketPath)
	}
	return socketPath, cleanup
}

// jsonHandler returns an http.HandlerFunc that responds with the given JSON.
func jsonHandler(statusCode int, body any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		data, _ := json.Marshal(body)
		_, _ = w.Write(data)
	}
}

func TestStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", jsonHandler(200, apiResponse[StatusResponse]{
		OK: true,
		Data: StatusResponse{
			Version: "1.0.0",
			Locked:  false,
			Uptime:  120,
			PID:     12345,
		},
	}))

	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()

	client := New(sock)
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}

	if status.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", status.Version, "1.0.0")
	}
	if status.Locked {
		t.Error("Locked = true, want false")
	}
	if status.Uptime != 120 {
		t.Errorf("Uptime = %d, want 120", status.Uptime)
	}
}

func TestIsRunning_true(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", jsonHandler(200, apiResponse[StatusResponse]{
		OK:   true,
		Data: StatusResponse{Version: "1.0.0"},
	}))

	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()

	client := New(sock)
	if !client.IsRunning(context.Background()) {
		t.Error("IsRunning() = false, want true")
	}
}

func TestIsRunning_false(t *testing.T) {
	client := New("/tmp/nonexistent-socket-test.sock")
	if client.IsRunning(context.Background()) {
		t.Error("IsRunning() = true, want false")
	}
}

func TestGetSSHKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/vault/ssh/myhost/root", jsonHandler(200, apiResponse[VaultSSHData]{
		OK: true,
		Data: VaultSSHData{
			PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----",
			PublicKey:  "ssh-ed25519 AAAA... myhost/root",
		},
	}))

	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()

	client := New(sock)
	data, err := client.GetSSHKey(context.Background(), "myhost", "root", "both")
	if err != nil {
		t.Fatalf("GetSSHKey() error: %v", err)
	}

	if data.PrivateKey == "" {
		t.Error("PrivateKey is empty")
	}
	if data.PublicKey == "" {
		t.Error("PublicKey is empty")
	}
}

func TestGetSSHKey_locked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/vault/ssh/myhost/root", jsonHandler(423, apiResponse[any]{
		OK:    false,
		Error: "Agent is locked",
		Code:  "AGENT_LOCKED",
	}))

	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()

	client := New(sock)
	_, err := client.GetSSHKey(context.Background(), "myhost", "root", "priv")
	if err == nil {
		t.Fatal("GetSSHKey() expected error, got nil")
	}
	if !IsLocked(err) {
		t.Errorf("IsLocked() = false for error: %v", err)
	}
}

func TestGetSSHKey_notFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/vault/ssh/unknown/user", jsonHandler(404, apiResponse[any]{
		OK:    false,
		Error: "No SSH key found for unknown/user",
		Code:  "NOT_FOUND",
	}))

	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()

	client := New(sock)
	_, err := client.GetSSHKey(context.Background(), "unknown", "user", "priv")
	if err == nil {
		t.Fatal("GetSSHKey() expected error, got nil")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound() = false for error: %v", err)
	}
}

func TestGetPassword(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/vault/password/example.com/admin", jsonHandler(200, apiResponse[VaultPasswordData]{
		OK:   true,
		Data: VaultPasswordData{Password: "secret123"},
	}))

	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()

	client := New(sock)
	data, err := client.GetPassword(context.Background(), "example.com", "admin")
	if err != nil {
		t.Fatalf("GetPassword() error: %v", err)
	}
	if data.Password != "secret123" {
		t.Errorf("Password = %q, want %q", data.Password, "secret123")
	}
}

func TestCreateSSHEntry(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/vault/ssh", jsonHandler(201, apiResponse[VaultSSHData]{
		OK:   true,
		Data: VaultSSHData{PublicKey: "ssh-ed25519 AAAA... new/entry"},
	}))

	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()

	client := New(sock)
	data, err := client.CreateSSHEntry(context.Background(), "new", "entry")
	if err != nil {
		t.Fatalf("CreateSSHEntry() error: %v", err)
	}
	if data.PublicKey == "" {
		t.Error("PublicKey is empty")
	}
}

func TestGetAddress(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/wallet/address", jsonHandler(200, apiResponse[WalletAddressData]{
		OK:   true,
		Data: WalletAddressData{Address: "0x1234abcd", Chain: "evm"},
	}))

	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()

	client := New(sock)
	data, err := client.GetAddress(context.Background(), "evm")
	if err != nil {
		t.Fatalf("GetAddress() error: %v", err)
	}
	if data.Address != "0x1234abcd" {
		t.Errorf("Address = %q, want %q", data.Address, "0x1234abcd")
	}
}

func TestAgentNotRunning(t *testing.T) {
	client := New("/tmp/nonexistent-socket-for-testing.sock")
	_, err := client.Status(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsNotRunning(err) {
		t.Errorf("IsNotRunning() = false for error: %v", err)
	}
}

func TestUnlockAndLock(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/unlock", jsonHandler(200, apiResponse[any]{OK: true}))
	mux.HandleFunc("/v1/lock", jsonHandler(200, apiResponse[any]{OK: true}))

	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()

	client := New(sock)

	if err := client.Unlock(context.Background(), "password", 30); err != nil {
		t.Fatalf("Unlock() error: %v", err)
	}

	if err := client.Lock(context.Background()); err != nil {
		t.Fatalf("Lock() error: %v", err)
	}
}
