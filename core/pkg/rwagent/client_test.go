package rwagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shortSocketBase holds test socket directories: short enough for sun_path.
const shortSocketBase = "/tmp"

// startMockAgent creates a mock agent server on a Unix socket for testing.
func startMockAgent(t *testing.T, handler http.Handler) (socketPath string, cleanup func()) {
	t.Helper()

	// t.TempDir() is too long for a Unix socket on macOS (sun_path holds
	// 104 bytes), so the socket lives in a short directory under /tmp.
	tmpDir, err := os.MkdirTemp(shortSocketBase, "rwt")
	if err != nil {
		t.Fatalf("create a socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })
	socketPath = filepath.Join(tmpDir, "test-agent.sock")

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatalf("chmod socket: %v", err)
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

func TestReleaseKey_decodesThePublicKey(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orama/release/key", jsonHandler(200, apiResponse[ReleaseKeyData]{
		OK:   true,
		Data: ReleaseKeyData{Purpose: "orama-release", KeyType: "ed25519", PublicKey: hex.EncodeToString(pub), Path: "m/1330790733'/1'/0'"},
	}))
	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()

	got, err := New(sock).ReleaseKey(context.Background())
	if err != nil {
		t.Fatalf("ReleaseKey() error: %v", err)
	}
	if !got.Equal(pub) {
		t.Errorf("key = %x, want %x", got, pub)
	}
}

func TestReleaseKey_refusals(t *testing.T) {
	cases := map[string]ReleaseKeyData{
		"wrong key type": {KeyType: "secp256k1", PublicKey: strings.Repeat("ab", ed25519.PublicKeySize)},
		"short key":      {KeyType: "ed25519", PublicKey: "abcd"},
		"not hex":        {KeyType: "ed25519", PublicKey: strings.Repeat("zz", ed25519.PublicKeySize)},
		"empty":          {KeyType: "ed25519"},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/orama/release/key", jsonHandler(200, apiResponse[ReleaseKeyData]{OK: true, Data: data}))
			sock, cleanup := startMockAgent(t, mux)
			defer cleanup()
			if _, err := New(sock).ReleaseKey(context.Background()); err == nil {
				t.Fatal("a release key the CLI cannot use was accepted")
			}
		})
	}
}

func TestReleaseKey_aHeadlessAgentHasNone(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/orama/release/key", jsonHandler(404, apiResponse[ReleaseKeyData]{OK: false, Error: "not found", Code: "NOT_FOUND"}))
	sock, cleanup := startMockAgent(t, mux)
	defer cleanup()
	if _, err := New(sock).ReleaseKey(context.Background()); err == nil {
		t.Fatal("a 404 was accepted as a key")
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

func TestDeleteSSHEntry(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		resp    apiResponse[struct{}]
		wantErr bool
	}{
		"deleted":          {200, apiResponse[struct{}]{OK: true}, false},
		"already gone":     {404, apiResponse[struct{}]{Code: CodeNotFound, Error: "no SSH key for h/u in the vault"}, false},
		"wallet is locked": {423, apiResponse[struct{}]{Code: "AGENT_LOCKED", Error: "locked"}, true},
	} {
		// No subtests: their names lengthen the temp dir, and a Unix socket
		// path over 104 bytes cannot be bound on macOS.
		func() {
			mux := http.NewServeMux()
			var method string
			mux.HandleFunc("/v1/vault/ssh/203.0.113.7/ubuntu", func(w http.ResponseWriter, r *http.Request) {
				method = r.Method
				jsonHandler(tc.status, tc.resp)(w, r)
			})
			sock, cleanup := startMockAgent(t, mux)
			defer cleanup()

			err := New(sock).DeleteSSHEntry(context.Background(), "203.0.113.7", "ubuntu")
			if (err != nil) != tc.wantErr {
				t.Errorf("%s: err = %v, wantErr %v", name, err, tc.wantErr)
			}
			if method != http.MethodDelete {
				t.Errorf("%s: method = %s, want DELETE", name, method)
			}
		}()
	}
}

func TestAgentSocketAllowed(t *testing.T) {
	if err := agentSocketAllowed(os.ModeSocket|0o600, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := agentSocketAllowed(os.ModeSocket|0o600, 2, 1); err == nil {
		t.Fatal("a socket owned by another uid was accepted")
	}
	if err := agentSocketAllowed(0o600, 1, 1); err == nil {
		t.Fatal("a regular file was accepted as the agent socket")
	}
	if err := agentSocketAllowed(os.ModeSymlink|0o777, 1, 1); err == nil {
		t.Fatal("a symlink was accepted as the agent socket")
	}
	if err := agentSocketAllowed(os.ModeSocket|0o660, 1, 1); err == nil {
		t.Fatal("a group-writable socket was accepted")
	}
	if err := agentSocketAllowed(os.ModeSocket|0o755, 1, 1); err != nil {
		t.Fatalf("0755 is what the agent creates, and other users cannot connect to it: %v", err)
	}
}

func TestCheckAgentSocket_refusesWhatAnotherUserCouldPlant(t *testing.T) {
	dir, err := os.MkdirTemp(shortSocketBase, "rwa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if err := os.Chmod(sock, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := checkAgentSocket(sock); err == nil {
		t.Fatal("dialled a world-accessible agent socket")
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkAgentSocket(sock); err != nil {
		t.Fatalf("the caller's socket: %v", err)
	}

	link := filepath.Join(dir, "b.sock")
	if err := os.Symlink(sock, link); err != nil {
		t.Fatal(err)
	}
	if err := checkAgentSocket(link); err == nil {
		t.Fatal("followed a symlink to the agent socket")
	}
	if err := checkAgentSocket(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing socket = %v, want not exist", err)
	}
}

const archiveMessageForPurposeTests = ArchiveMessageHeader + "\nversion: 1.0.0\ncommit: abcdef0\narch: amd64\ndate: 2026-01-01T00:00:00Z\nsigners: none\nmanifest sha256: " +
	"0000000000000000000000000000000000000000000000000000000000000000"

const releasePayloadForPurposeTests = `{"_type":"targets","expires":"2027-01-01T00:00:00Z","spec_version":"1.0.31","targets":{},"version":1}`

func TestSignForPurpose_refusesAMessageInTheOtherPurposesFormat(t *testing.T) {
	// No socket exists, so a refusal that is not ErrPurposeMismatch would be a
	// dial error: the check must run before the agent is contacted.
	c := New(t.TempDir() + "/none.sock")
	cases := map[string]struct{ message, purpose string }{
		"release payload as archive": {releasePayloadForPurposeTests, PurposeOramaArchive},
		"archive message as release": {archiveMessageForPurposeTests, PurposeOramaRelease},
		"plain text as release":      {"hello", PurposeOramaRelease},
		"plain text as archive":      {"hello", PurposeOramaArchive},
		"empty message as release":   {"", PurposeOramaRelease},
		"unknown purpose":            {releasePayloadForPurposeTests, "orama-other"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := c.SignForPurpose(context.Background(), tc.message, "evm", tc.purpose)
			if !errors.Is(err, ErrPurposeMismatch) {
				t.Fatalf("err = %v, want ErrPurposeMismatch", err)
			}
		})
	}
}

func TestSign_refusesEveryRegisteredFormat(t *testing.T) {
	c := New(t.TempDir() + "/none.sock")
	for name, message := range map[string]string{
		"archive": archiveMessageForPurposeTests,
		"release": releasePayloadForPurposeTests,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.Sign(context.Background(), message, "evm")
			if !errors.Is(err, ErrPurposeMismatch) {
				t.Fatalf("err = %v, want ErrPurposeMismatch", err)
			}
		})
	}
}

func TestReservedPurpose(t *testing.T) {
	cases := map[string]string{
		archiveMessageForPurposeTests: PurposeOramaArchive,
		releasePayloadForPurposeTests: PurposeOramaRelease,
		"Sign in to gateway":          "",
		"":                            "",
	}
	for message, want := range cases {
		if got := ReservedPurpose(message); got != want {
			t.Errorf("ReservedPurpose(%.30q) = %q, want %q", message, got, want)
		}
	}
}
