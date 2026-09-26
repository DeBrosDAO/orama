package pubsub

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// shortSocketPath is a socket path inside the platform's limit (104 bytes on
// darwin), which t.TempDir's long names can exceed.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ps")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "pubsub.sock")
}

// The API is served on a socket only its owner can open, and the listener
// admits a caller running as the service's own user.
func TestListenSocket_admitsTheServiceUser(t *testing.T) {
	sock := shortSocketPath(t)
	ln, err := ListenSocket(sock, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != socketMode {
		t.Errorf("socket mode %v, want %v", info.Mode().Perm(), os.FileMode(socketMode))
	}

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })}
	go srv.Serve(ln)
	defer srv.Close()

	c := NewHTTPClient(sock, "ns", zap.NewNop())
	resp, err := c.http.Get(socketBaseURL + "/health")
	if err != nil {
		t.Fatalf("the service's own user was refused: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status %d", resp.StatusCode)
	}
}

// The finding: anything on the node could reach the API. A caller running as
// any other user is dropped before a byte is read.
func TestPeerCredListener_dropsAnotherUser(t *testing.T) {
	sock := shortSocketPath(t)
	raw, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln := &peerCredListener{
		Listener: raw,
		uid:      uint32(os.Getuid()) + 1,
		peerUID:  peerUID,
		logger:   zap.NewNop(),
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("a connection from another uid was kept open")
	}
	select {
	case c := <-accepted:
		c.Close()
		t.Fatal("a connection from another uid was accepted")
	default:
	}
}

// A caller whose credentials cannot be read is dropped, not admitted.
func TestPeerCredListener_dropsAnUnidentifiedCaller(t *testing.T) {
	sock := shortSocketPath(t)
	raw, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	l := &peerCredListener{
		uid:     uint32(os.Getuid()),
		peerUID: func(*net.UnixConn) (uint32, error) { return 0, os.ErrPermission },
	}
	go func() {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			defer conn.Close()
			time.Sleep(time.Second)
		}
	}()
	conn, err := raw.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := l.admit(conn); err == nil {
		t.Fatal("a caller whose uid could not be read was admitted")
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if err := l.admit(a); err == nil {
		t.Fatal("a connection that is not a unix socket was admitted")
	}
}

// Anything but a socket at the path is left alone and refused.
func TestListenSocket_refusesToReplaceAFile(t *testing.T) {
	sock := shortSocketPath(t)
	if err := os.WriteFile(sock, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenSocket(sock, zap.NewNop()); err == nil {
		t.Fatal("a regular file at the socket path was replaced")
	}
	if data, _ := os.ReadFile(sock); string(data) != "not a socket" {
		t.Error("the file was modified")
	}
}

// A socket left by a previous run does not stop the next one from starting.
func TestListenSocket_replacesAStaleSocket(t *testing.T) {
	sock := shortSocketPath(t)
	old, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if ul, ok := old.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	old.Close()
	ln, err := ListenSocket(sock, zap.NewNop())
	if err != nil {
		t.Fatalf("a stale socket blocked the listener: %v", err)
	}
	ln.Close()
}

// The unit creates the runtime directory the socket lives in.
func TestPubsubUnit_runtimeDirectoryHoldsTheSocket(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "systemd", "orama-namespace-pubsub@.service"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	if filepath.Dir(filepath.Dir(DefaultSocketPath)) != "/run" {
		t.Fatalf("DefaultSocketPath %s is not directly in a /run runtime directory", DefaultSocketPath)
	}
	for _, want := range []string{
		"RuntimeDirectory=" + filepath.Base(filepath.Dir(DefaultSocketPath)) + "\n",
		"RuntimeDirectoryMode=0700\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("the pubsub unit lacks %q", strings.TrimSpace(want))
		}
	}
}
