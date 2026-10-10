package remotessh

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func TestTunnelArgs_forwardsALoopbackPortToTheRemoteAddress(t *testing.T) {
	node := inspector.Node{Host: "203.0.113.7", User: "deploy", SSHKey: "/tmp/key"}

	args := strings.Join(tunnelArgs(node, 40123, "198.18.0.2:31003"), " ")

	for _, want := range []string{
		"-L 127.0.0.1:40123:198.18.0.2:31003", "ExitOnForwardFailure=yes", "BatchMode=yes", " -N ",
		"-i /tmp/key", "deploy@203.0.113.7", "StrictHostKeyChecking=accept-new", "IdentitiesOnly=yes",
	} {
		if !strings.Contains(args+" ", want) {
			t.Errorf("tunnel args lack %q:\n%s", want, args)
		}
	}
	if !strings.HasSuffix(args, "deploy@203.0.113.7") {
		t.Errorf("the destination must come last: %s", args)
	}
}

func TestOpenTunnel_withoutAKeyIsRefused(t *testing.T) {
	_, err := OpenTunnel(context.Background(), inspector.Node{Host: "h", User: "root"}, "127.0.0.1:1")

	if err == nil || !strings.Contains(err.Error(), "PrepareNodeKeys") {
		t.Fatalf("err = %v, want the missing key named", err)
	}
}

func TestFreeLoopbackPort_isUsable(t *testing.T) {
	port, err := freeLoopbackPort()
	if err != nil || port <= 0 {
		t.Fatalf("freeLoopbackPort = %d, %v", port, err)
	}
	l, err := net.Listen("tcp", net.JoinHostPort(loopbackHost, strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("the port %d returned is not free: %v", port, err)
	}
	_ = l.Close()
}

// startedTunnel wraps a long-running process as a Tunnel whose local end is addr.
func startedTunnel(t *testing.T, addr string, program string, args ...string) *Tunnel {
	t.Helper()
	cmd := exec.Command(program, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tun := &Tunnel{Addr: addr, cmd: cmd, done: make(chan error, 1)}
	go func() { tun.done <- cmd.Wait() }()
	t.Cleanup(func() { _ = tun.Close() })
	return tun
}

func TestTunnel_waitReadyReturnsOnceTheLocalEndAccepts(t *testing.T) {
	l, err := net.Listen("tcp", net.JoinHostPort(loopbackHost, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	tun := startedTunnel(t, l.Addr().String(), "sleep", "30")

	if err := tun.waitReady(context.Background(), inspector.Node{Host: "h"}, "r:1"); err != nil {
		t.Fatalf("waitReady: %v", err)
	}
}

func TestTunnel_waitReadyReportsAnSSHThatExitedFirst(t *testing.T) {
	tun := startedTunnel(t, net.JoinHostPort(loopbackHost, "1"), "false")

	err := tun.waitReady(context.Background(), inspector.Node{Host: "203.0.113.7"}, "198.18.0.2:31003")

	if err == nil || !strings.Contains(err.Error(), "exited before it was ready") || !strings.Contains(err.Error(), "203.0.113.7") {
		t.Fatalf("err = %v, want the early exit named with the node", err)
	}
}

func TestTunnel_waitReadyStopsWhenTheContextEnds(t *testing.T) {
	tun := startedTunnel(t, net.JoinHostPort(loopbackHost, "1"), "sleep", "30")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := tun.waitReady(ctx, inspector.Node{Host: "h"}, "r:1")

	if err == nil || !strings.Contains(err.Error(), "was not ready") {
		t.Fatalf("err = %v, want the timeout named", err)
	}
}

func TestUploadBytes_copiesTheBytesAndLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	copied := filepath.Join(dir, "copied")
	argsFile := filepath.Join(dir, "args")
	// A fake scp: the local file is the second to last argument.
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nfor a; do prev=$last; last=$a; done\ncp \"$prev\" " + copied + "\n"
	if err := os.WriteFile(filepath.Join(dir, "scp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	node := inspector.Node{Host: "1.2.3.4", User: "root", SSHKey: "/dev/null"}

	if err := UploadBytes(node, []byte("root-bytes"), "/tmp/x/root.json"); err != nil {
		t.Fatalf("UploadBytes: %v", err)
	}

	got, err := os.ReadFile(copied)
	if err != nil || string(got) != "root-bytes" {
		t.Errorf("uploaded %q, %v", got, err)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "root@1.2.3.4:/tmp/x/root.json") {
		t.Errorf("scp args lack the destination:\n%s", args)
	}
	local := strings.Fields(string(args))
	temp := local[len(local)-2]
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Errorf("the temporary copy %s was left behind", temp)
	}
}

func TestUploadBytes_aFailedCopyIsAnErrorAndStillCleansUp(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "scp"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := UploadBytes(inspector.Node{Host: "1.2.3.4", User: "root", SSHKey: "/dev/null"}, []byte("x"), "/tmp/x")

	if err == nil || !strings.Contains(err.Error(), "SCP to 1.2.3.4 failed") {
		t.Fatalf("err = %v", err)
	}
	args, _ := os.ReadFile(argsFile)
	fields := strings.Fields(string(args))
	if _, statErr := os.Stat(fields[len(fields)-2]); !os.IsNotExist(statErr) {
		t.Error("the temporary copy was left behind after a failed upload")
	}
}

func TestTunnel_closeMayBeCalledTwiceAndAfterTheProcessExited(t *testing.T) {
	tun := startedTunnel(t, net.JoinHostPort(loopbackHost, "1"), "true")
	// The process ends on its own; Close still has to return, twice.
	if err := tun.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := tun.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestTunnel_closeKillsARunningForward(t *testing.T) {
	tun := startedTunnel(t, net.JoinHostPort(loopbackHost, "1"), "sleep", "30")
	done := make(chan error, 1)

	go func() { done <- tun.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return: the forward was not killed")
	}
}
