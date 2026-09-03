package namespace

import (
	"net"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// Bugboard #276. The port allocator picks a block using only the
// namespace_port_allocations table, so it cannot see a process holding the port
// without a matching row — an orphaned namespace, or any other listener. On devnet
// anchat-v2 was allocated 10005-10009 on all three nodes while an orphaned
// `rootwallet` namespace was already bound there; the spawned services crash-looped
// ("bind: address already in use", restart counter climbing past 19) while
// provisioning still reported the cluster ready. On the one node where the ports
// happened to be free, the collision escalated into joining a FOREIGN namespace's
// raft group (bugboard #275).
//
// Refusing to start, naming the port, turns silent corruption into an actionable
// error.

// listenOn occupies a port for the duration of the test and returns it.
func listenOn(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

// freePort returns a port number nothing is listening on.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func TestEnsurePortsFree_passesWhenPortsAreFree(t *testing.T) {
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	if err := s.ensurePortsFree("anchat-v2", map[string]int{
		"RQLite HTTP": freePort(t),
		"RQLite Raft": freePort(t),
	}); err != nil {
		t.Errorf("ensurePortsFree failed on free ports: %v", err)
	}
}

// The reproduction: a port held by something else must be reported, not bound.
func TestEnsurePortsFree_failsWhenPortIsOccupied(t *testing.T) {
	occupied := listenOn(t)
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	start := time.Now()
	err := s.ensurePortsFree("anchat-v2", map[string]int{"RQLite Raft": occupied})
	if err == nil {
		t.Fatal("ensurePortsFree succeeded on an occupied port — the service would crash-loop on bind while provisioning reported success")
	}
	if !strings.Contains(err.Error(), "anchat-v2") {
		t.Errorf("error does not name the namespace: %v", err)
	}
	if !strings.Contains(err.Error(), "RQLite Raft") {
		t.Errorf("error does not name which service: %v", err)
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error does not say the port is in use: %v", err)
	}
	// It must give up rather than block forever.
	if elapsed := time.Since(start); elapsed > portFreeWaitTimeout+5*time.Second {
		t.Errorf("waited %v, expected to give up around %v", elapsed, portFreeWaitTimeout)
	}
}

// Zero/unset ports are skipped — not every service uses every port.
func TestEnsurePortsFree_ignoresUnsetPorts(t *testing.T) {
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	if err := s.ensurePortsFree("ns", map[string]int{"TURN TLS": 0, "SFU": -1}); err != nil {
		t.Errorf("unset ports should be skipped, got: %v", err)
	}
}

// A port released while we wait (the normal restart case: systemd returns before
// the socket is fully closed) must be accepted rather than failing the restart.
func TestEnsurePortsFree_acceptsPortReleasedDuringWait(t *testing.T) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		time.Sleep(1500 * time.Millisecond)
		_ = ln.Close()
	}()

	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())
	if err := s.ensurePortsFree("anchat-test", map[string]int{"Gateway HTTP": port}); err != nil {
		t.Errorf("a port released during the wait should be accepted (this is what a restart looks like), got: %v", err)
	}
}

func TestPortInUse_reflectsRealState(t *testing.T) {
	if portInUse(freePort(t)) {
		t.Error("portInUse = true for a free port")
	}
	if !portInUse(listenOn(t)) {
		t.Error("portInUse = false for an occupied port")
	}
}
