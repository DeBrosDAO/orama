package namespace

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func newTeardownSpawner(units, state func(string) error) *SystemdSpawner {
	return &SystemdSpawner{logger: zap.NewNop(), teardownUnitsFn: units, deleteStateFn: state}
}

// Units first, then the state that a restart discovers the namespace from.
func TestTeardownNamespace_stopsUnitsThenDeletesState(t *testing.T) {
	var order []string
	s := newTeardownSpawner(
		func(ns string) error { order = append(order, "units:"+ns); return nil },
		func(ns string) error { order = append(order, "state:"+ns); return nil })

	if err := s.TeardownNamespace(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"units:acme", "state:acme"}) {
		t.Fatalf("order = %v", order)
	}
}

// The state is the retry handle: deleting it under a unit that could not be
// stopped would leave a running process nobody can find or tear down again.
func TestTeardownNamespace_keepsStateWhenAUnitCannotBeTornDown(t *testing.T) {
	deleted := false
	s := newTeardownSpawner(
		func(string) error { return errors.New("disable failed") },
		func(string) error { deleted = true; return nil })

	err := s.TeardownNamespace(context.Background(), "acme")
	if err == nil || !strings.Contains(err.Error(), "disable failed") {
		t.Fatalf("err = %v, want the unit failure", err)
	}
	if deleted {
		t.Fatal("the namespace's state was deleted although its units were not torn down")
	}
}

func TestTeardownNamespace_reportsAStateFailure(t *testing.T) {
	s := newTeardownSpawner(
		func(string) error { return nil },
		func(string) error { return errors.New("rm failed") })

	if err := s.TeardownNamespace(context.Background(), "acme"); err == nil || !strings.Contains(err.Error(), "rm failed") {
		t.Fatalf("err = %v, want the state failure", err)
	}
}

func TestTeardownNamespace_refusesThePlatformNamespaces(t *testing.T) {
	called := false
	s := newTeardownSpawner(
		func(string) error { called = true; return nil },
		func(string) error { called = true; return nil })

	for _, ns := range []string{"", "index", "nameserver", "system", "default", " index "} {
		if err := s.TeardownNamespace(context.Background(), ns); err == nil {
			t.Errorf("%q was torn down", ns)
		}
	}
	if called {
		t.Fatal("a platform namespace reached the unit or state teardown")
	}
}

// Remote nodes get the teardown action, not a stop: a plain stop leaves the
// unit enabled and the next upgrade starts it again.
func TestTeardownNamespaceOnNode_remoteSendsTheTeardownAction(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)

	err := sw.cm.teardownNamespaceOnNode(context.Background(), staleClusterNode{NodeID: "n1", InternalIP: "10.0.0.1"}, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if sw.count("stop:teardown-namespace") != 1 || sw.count("stop:") != 1 {
		t.Fatalf("events = %v", sw.events)
	}
}

func TestTeardownNamespaceOnNode_remoteWithoutAnOverlayAddressIsAnError(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)

	err := sw.cm.teardownNamespaceOnNode(context.Background(), staleClusterNode{NodeID: "n1"}, "acme")
	if err == nil || len(sw.events) != 0 {
		t.Fatalf("err = %v, events = %v; want an error and no request", err, sw.events)
	}
}

func TestTeardownNamespaceOnNode_localUsesTheSpawner(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)
	var tornDown []string
	sw.cm.systemdSpawner = newTeardownSpawner(
		func(ns string) error { tornDown = append(tornDown, ns); return nil },
		func(string) error { return nil })

	if err := sw.cm.teardownNamespaceOnNode(context.Background(), staleClusterNode{NodeID: "local"}, "acme"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tornDown, []string{"acme"}) || len(sw.events) != 0 {
		t.Fatalf("tornDown = %v, remote events = %v", tornDown, sw.events)
	}
}

func TestTeardownNamespaceOnNodes_attemptsEveryNodeAndJoinsFailures(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)
	sw.stopErr = func(string) error { return errors.New("node unreachable") }

	err := sw.cm.teardownNamespaceOnNodes(context.Background(), twoRemoteNodes, "acme")
	if err == nil || strings.Count(err.Error(), "node unreachable") != 2 {
		t.Fatalf("err = %v, want both nodes' failures", err)
	}
	if sw.count("stop:teardown-namespace") != 2 {
		t.Fatalf("events = %v, want an attempt on each node", sw.events)
	}
}

func TestTeardownNamespaceOnNodes_noNodes(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)
	if err := sw.cm.teardownNamespaceOnNodes(context.Background(), nil, "acme"); err != nil || len(sw.events) != 0 {
		t.Fatalf("err = %v, events = %v", err, sw.events)
	}
}

// The stagenet bug: a rolled-back provisioning left enabled units and on-disk
// state on every node, and withdrew the membership a later delete uses to find
// them. The rollback itself must remove the namespace from every node, before
// the ports it held are released.
func TestRollbackProvisioning_tearsTheNamespaceDownOnEveryNode(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)
	c := &NamespaceCluster{ID: "c1", NamespaceName: "acme"}
	nodes := []NodeCapacity{{NodeID: "n1", InternalIP: "10.0.0.1"}, {NodeID: "n2", InternalIP: "10.0.0.2"}}

	sw.cm.rollbackProvisioning(context.Background(), c, nodes, nil, "boom")

	if got := sw.count("stop:teardown-namespace"); got != 2 {
		t.Fatalf("teardowns = %d, want one per node (events %v)", got, sw.events)
	}
	if sw.count("stop:") != 2 {
		t.Fatalf("a plain stop was sent, which leaves units enabled: %v", sw.events)
	}
	if sw.index("stop:") > sw.index("exec:DELETE FROM namespace_port_allocations") {
		t.Fatalf("ports were released before the nodes were torn down: %v", sw.events)
	}
}

func TestRollbackProvisioning_aFailedTeardownDoesNotStopTheRollback(t *testing.T) {
	sw := newStaleSweep(nil, nil, false)
	sw.stopErr = func(string) error { return errors.New("node unreachable") }
	c := &NamespaceCluster{ID: "c1", NamespaceName: "acme"}

	sw.cm.rollbackProvisioning(context.Background(), c, []NodeCapacity{{NodeID: "n1", InternalIP: "10.0.0.1"}}, nil, "boom")

	if countExecs(sw.db.recoveryMockDB, "INSERT INTO namespace_pending_cleanup") == 0 {
		t.Fatal("the failed teardown was not recorded for replay")
	}
	if countExecs(sw.db.recoveryMockDB, "DELETE FROM namespace_port_allocations") == 0 {
		t.Fatal("the rollback stopped at a node it could not reach")
	}
}
