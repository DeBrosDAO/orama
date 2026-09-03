package namespace

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/systemd"
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

	if err := s.ensurePortsFree("anchat-v2", systemd.ServiceTypeRQLite, map[string]int{
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
	err := s.ensurePortsFree("anchat-v2", systemd.ServiceTypeRQLite, map[string]int{"RQLite Raft": occupied})
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

	if err := s.ensurePortsFree("ns", systemd.ServiceTypeTURN, map[string]int{"TURN TLS": 0, "SFU": -1}); err != nil {
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
	if err := s.ensurePortsFree("anchat-test", systemd.ServiceTypeGateway, map[string]int{"Gateway HTTP": port}); err != nil {
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

// Spawning is a reconcile: the boot supervisor calls it again after any
// failure, and by then the unit started by the previous attempt is holding its
// own port. Reporting that as a conflict wedges the node permanently, because
// no amount of retrying can free a port the service is legitimately using.
func TestEnsurePortsFree_ownRunningUnitIsNotAConflict(t *testing.T) {
	occupied := listenOn(t)

	original := serviceIsActive
	serviceIsActive = func(*systemd.Manager, string, systemd.ServiceType) (bool, error) { return true, nil }
	t.Cleanup(func() { serviceIsActive = original })

	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	start := time.Now()
	if err := s.ensurePortsFree("anchat-v2", systemd.ServiceTypeRQLite, map[string]int{"RQLite Raft": occupied}); err != nil {
		t.Fatalf("a port held by the already-running unit must not be a conflict, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the already-active check must short-circuit, took %v", elapsed)
	}
}

// "Unit is active" is not on its own a reason to skip the check. For a
// Type=simple unit it only proves the process exists, so a service still
// running on a previous port allocation must not exempt a different block.
func TestEnsurePortsFree_activeUnitDoesNotExemptPortsItIsNotHolding(t *testing.T) {
	original := serviceIsActive
	serviceIsActive = func(*systemd.Manager, string, systemd.ServiceType) (bool, error) { return true, nil }
	t.Cleanup(func() { serviceIsActive = original })

	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	// The unit is "active", but a foreign process holds one of the ports it is
	// now being asked to bind. That is bug-276 and must still be refused.
	foreign := listenOn(t)
	err := s.ensurePortsFree("anchat-v2", systemd.ServiceTypeRQLite, map[string]int{
		"RQLite HTTP": freePort(t),
		"RQLite Raft": foreign,
	})
	if err == nil {
		t.Fatal("an active unit must not exempt a port block it is not holding")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error should name the conflict: %v", err)
	}
}

// A foreign listener is still a conflict when this service is not running.
func TestEnsurePortsFree_stillFailsWhenOurUnitIsNotRunning(t *testing.T) {
	occupied := listenOn(t)

	original := serviceIsActive
	serviceIsActive = func(*systemd.Manager, string, systemd.ServiceType) (bool, error) { return false, nil }
	t.Cleanup(func() { serviceIsActive = original })

	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())

	if err := s.ensurePortsFree("anchat-v2", systemd.ServiceTypeRQLite, map[string]int{"RQLite Raft": occupied}); err == nil {
		t.Fatal("a port held by a foreign process must still be reported")
	}
}

// The idempotence short-circuit must not extend to a fresh start. "Fresh" means
// the raft directory is about to be deleted, so an already-running unit is a
// leftover namespace still holding that data (bugboard #275/#281), not this
// call's own service.
func TestSpawnRQLite_freshStartRefusesARunningUnit(t *testing.T) {
	original := serviceIsActive
	serviceIsActive = func(*systemd.Manager, string, systemd.ServiceType) (bool, error) { return true, nil }
	t.Cleanup(func() { serviceIsActive = original })

	base := t.TempDir()
	raftDir := filepath.Join(base, "anchat-v2", "rqlite", "node-1")
	if err := os.MkdirAll(raftDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	marker := filepath.Join(raftDir, "raft.db")
	if err := os.WriteFile(marker, []byte("existing raft state"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := NewSystemdSpawner(base, "", zap.NewNop())
	err := s.SpawnRQLite(context.Background(), "anchat-v2", "node-1", rqlite.InstanceConfig{
		FreshStart: true,
		HTTPPort:   freePort(t),
		RaftPort:   freePort(t),
	})
	if err == nil {
		t.Fatal("a fresh start against a running unit must be refused")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error should say the unit is already running: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Errorf("the refusal must happen before the raft directory is cleared, got: %v", statErr)
	}
}
