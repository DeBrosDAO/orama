package systemd

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/unitenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type fakeUnits struct {
	active map[string]bool
	since  time.Time
	calls  []string
}

func newFakeManager(t *testing.T) (*Manager, *fakeUnits) {
	t.Helper()
	f := &fakeUnits{active: map[string]bool{}}
	m := NewManager(t.TempDir(), zap.NewNop())
	m.unitEnvDir = t.TempDir()
	m.writeUnitEnv = func(ns, svc, contents string) error {
		return unitenv.Write(m.unitEnvDir, ns, svc, []byte(contents), unitenv.Owner{UID: os.Getuid(), GID: os.Getgid()})
	}
	m.unitActive = func(u string) bool { return f.active[u] }
	m.activeSince = func(string) (time.Time, error) { return f.since, nil }
	m.runUnitCmd = func(args ...string) ([]byte, error) {
		f.calls = append(f.calls, strings.Join(args, " "))
		f.active[args[1]] = true
		return nil, nil
	}
	return m, f
}

// A re-run install rewrote the gateway's inputs while it ran; `systemctl start`
// was a no-op and the gateway stayed on the previous run's credentials.
func TestStartService_RestartsARunningUnitWhoseEnvChanged(t *testing.T) {
	m, f := newFakeManager(t)
	unit := m.serviceName("index", ServiceTypeGateway)
	f.active[unit] = true
	f.since = time.Now().Add(-time.Hour)

	if err := m.GenerateEnvFile("index", "n1", ServiceTypeGateway, map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := m.StartService("index", ServiceTypeGateway); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, "|") != "restart "+unit {
		t.Fatalf("calls = %v, want a restart", f.calls)
	}
}

// Nothing changed: a running unit is left alone — no restart on every
// reconcile tick.
func TestStartService_LeavesAnUnchangedRunningUnitAlone(t *testing.T) {
	m, f := newFakeManager(t)
	unit := m.serviceName("index", ServiceTypeSFU)
	env := map[string]string{"B": "2", "A": "1", "C": "3"}
	if err := m.GenerateEnvFile("index", "n1", ServiceTypeSFU, env); err != nil {
		t.Fatal(err)
	}
	if err := m.StartService("index", ServiceTypeSFU); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	f.since = time.Now().Add(time.Second) // started after the env was written

	// Same inputs, possibly in a different map order.
	if err := m.GenerateEnvFile("index", "n1", ServiceTypeSFU, map[string]string{"C": "3", "A": "1", "B": "2"}); err != nil {
		t.Fatal(err)
	}
	if err := m.StartService("index", ServiceTypeSFU); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("an unchanged running unit must not be touched, got %v", f.calls)
	}
	if !f.active[unit] {
		t.Fatal("unit should still be active")
	}
}

func TestStartService_MarkedConfigRestarts(t *testing.T) {
	m, f := newFakeManager(t)
	unit := m.serviceName("anchat", ServiceTypeGateway)
	f.active[unit] = true
	f.since = time.Now().Add(time.Hour)
	m.MarkConfigChanged("anchat", ServiceTypeGateway)
	if err := m.StartService("anchat", ServiceTypeGateway); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, "|") != "restart "+unit {
		t.Fatalf("calls = %v", f.calls)
	}
	f.calls = nil
	if err := m.StartService("anchat", ServiceTypeGateway); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("the mark is consumed by the restart, got %v", f.calls)
	}
}

// Env content is rendered in a fixed order so identical inputs are identical
// bytes; otherwise every write looked like a change.
func TestGenerateEnvFile_IsDeterministic(t *testing.T) {
	m, _ := newFakeManager(t)
	env := map[string]string{"Z": "26", "A": "1", "M": "13"}
	if err := m.GenerateEnvFile("index", "n1", ServiceTypeSFU, env); err != nil {
		t.Fatal(err)
	}
	path := m.envFilePath("index", ServiceTypeSFU)
	first, _ := os.ReadFile(path)
	if !strings.Contains(string(first), "A=1\nM=13\nZ=26\n") {
		t.Errorf("keys not sorted:\n%s", first)
	}
}

func TestParseActiveEnter(t *testing.T) {
	got, err := parseActiveEnter("Sat 2026-09-26 00:44:55.123456 UTC")
	if err != nil || got.Nanosecond() != 123456000 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := parseActiveEnter("n/a"); err == nil {
		t.Error("an unparseable timestamp must be an error")
	}
}

// The reconcile runs on every node; restarting rqlite voters as a side effect
// of an env rewrite (e.g. JOIN_ARGS dropping -join once raft state exists)
// could restart several voters together.
func TestStartService_NeverRestartsAStatefulClusterService(t *testing.T) {
	for _, st := range []ServiceType{ServiceTypeRQLite, ServiceTypeOlric, ServiceTypeIPFS, ServiceTypeIPFSCluster, ServiceTypeVault, ServiceTypeWireGuard} {
		m, f := newFakeManager(t)
		unit := m.serviceName("index", st)
		f.active[unit] = true
		f.since = time.Now().Add(-time.Hour)
		m.MarkConfigChanged("index", st)
		if err := m.StartService("index", st); err != nil {
			t.Fatal(err)
		}
		if len(f.calls) != 0 {
			t.Errorf("%s: a running stateful service must not be restarted implicitly, got %v", st, f.calls)
		}
	}
}

// The reconcile calls StartService every pass, and a stateful unit's changed
// inputs stay pending until an operator restarts it. The warning used to be
// logged on every pass; it is logged once per change.
func TestStartService_DeferredRestartIsReportedOncePerChange(t *testing.T) {
	m, f := newFakeManager(t)
	core, logs := observer.New(zap.WarnLevel)
	m.logger = zap.New(core)
	unit := m.serviceName("index", ServiceTypeRQLite)
	f.active[unit] = true
	f.since = time.Now().Add(-time.Hour)

	m.MarkConfigChanged("index", ServiceTypeRQLite)
	for i := 0; i < 3; i++ {
		if err := m.StartService("index", ServiceTypeRQLite); err != nil {
			t.Fatal(err)
		}
	}
	if n := logs.FilterMessageSnippet("next rolling restart").Len(); n != 1 {
		t.Fatalf("logged %d times over three passes, want 1", n)
	}

	// A second change is news.
	m.MarkConfigChanged("index", ServiceTypeRQLite)
	if err := m.StartService("index", ServiceTypeRQLite); err != nil {
		t.Fatal(err)
	}
	if n := logs.FilterMessageSnippet("next rolling restart").Len(); n != 2 {
		t.Fatalf("a new change was not reported: %d warnings, want 2", n)
	}
	if len(f.calls) != 0 {
		t.Fatalf("a stateful unit was restarted: %v", f.calls)
	}
}

// Once the unit is (re)started through StartService, the pending change is
// consumed; a later change is reported again.
func TestStartService_DeferralClearedByStart(t *testing.T) {
	m, f := newFakeManager(t)
	core, logs := observer.New(zap.WarnLevel)
	m.logger = zap.New(core)
	unit := m.serviceName("index", ServiceTypeOlric)
	f.active[unit] = true
	f.since = time.Now().Add(-time.Hour)

	m.MarkConfigChanged("index", ServiceTypeOlric)
	if err := m.StartService("index", ServiceTypeOlric); err != nil {
		t.Fatal(err)
	}
	f.active[unit] = false // stopped for a rolling restart
	if err := m.StartService("index", ServiceTypeOlric); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.calls, "|") != "start "+unit {
		t.Fatalf("calls = %v, want one start", f.calls)
	}
	m.MarkConfigChanged("index", ServiceTypeOlric)
	if err := m.StartService("index", ServiceTypeOlric); err != nil {
		t.Fatal(err)
	}
	if n := logs.FilterMessageSnippet("next rolling restart").Len(); n != 2 {
		t.Fatalf("got %d warnings, want one per change", n)
	}
}
