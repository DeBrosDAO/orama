package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const agentChildEnv = "STAGENET_NODE_TEST_AGENT_SOCKET"

// TestMain runs the agent as a child when asked, so the test can signal a real process.
func TestMain(m *testing.M) {
	if path := os.Getenv(agentChildEnv); path != "" {
		ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
		defer stop()
		spec := listenSpec{Path: path, UID: os.Getuid()}
		if err := serveAgent(ctx, http.NotFoundHandler(), []listenSpec{spec}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// An agent ends when the ssh session that started it closes, with SIGHUP. It must remove its
// socket then: a stale socket file made the next smoke run's readiness check pass before the new
// agent was listening, and its first request failed with EOF.
func TestAgent_aHangUpRemovesItsSocket(t *testing.T) {
	// A short directory: a unix socket path is limited to about 100 bytes, and t.TempDir() on macOS
	// is longer than that.
	dir, err := os.MkdirTemp("/tmp", "sna")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "agent.sock")
	child := exec.Command(os.Args[0], "-test.run=^$")
	child.Env = append(os.Environ(), agentChildEnv+"="+sock)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if fi, err := os.Lstat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
			break
		}
		if time.Now().After(deadline) {
			_ = child.Process.Kill()
			t.Fatal("the agent's socket did not appear")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := child.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("the agent did not exit cleanly on SIGHUP: %v", err)
	}
	if _, err := os.Lstat(sock); !os.IsNotExist(err) {
		t.Fatalf("the socket is still there after SIGHUP (%v)", err)
	}
}
