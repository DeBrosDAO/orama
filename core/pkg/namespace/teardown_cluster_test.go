package namespace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// spawnerWithState is a spawner whose namespace "acme" has a cluster-state.json
// naming stateCluster ("" writes none).
func spawnerWithState(t *testing.T, stateCluster string, tornDown *bool) *SystemdSpawner {
	t.Helper()
	base := t.TempDir()
	if stateCluster != "" {
		dir := filepath.Join(base, "acme")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cluster-state.json"), []byte(`{"cluster_id":"`+stateCluster+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &SystemdSpawner{
		logger:             zap.NewNop(),
		namespaceBase:      base,
		teardownUnitsFn:    func(string) error { *tornDown = true; return nil },
		deleteStateFn:      func(string) error { return nil },
		removeTenantDataFn: func(string) error { return nil },
	}
}

// A teardown owed for the deleted namespace reaches a node that holds the name
// again, for a new cluster: it would delete the new namespace.
func TestTeardownNamespaceOfCluster_refusesAnotherClustersNamespace(t *testing.T) {
	var tornDown bool
	s := spawnerWithState(t, "c-new", &tornDown)

	err := s.TeardownNamespaceOfCluster(context.Background(), "acme", "c-old", true)
	if !errors.Is(err, ErrClusterMismatch) {
		t.Fatalf("err = %v, want ErrClusterMismatch", err)
	}
	if tornDown {
		t.Fatal("the new namespace was torn down")
	}
}

func TestTeardownNamespaceOfCluster_tearsDownItsOwnCluster(t *testing.T) {
	var tornDown bool
	s := spawnerWithState(t, "c1", &tornDown)
	if err := s.TeardownNamespaceOfCluster(context.Background(), "acme", "c1", false); err != nil || !tornDown {
		t.Fatalf("err = %v, torn down = %v", err, tornDown)
	}
}

// No state on the node: nothing to compare, and the teardown is the node's to do.
func TestTeardownNamespaceOfCluster_withNoStateGoesAhead(t *testing.T) {
	var tornDown bool
	s := spawnerWithState(t, "", &tornDown)
	if err := s.TeardownNamespaceOfCluster(context.Background(), "acme", "c1", false); err != nil || !tornDown {
		t.Fatalf("err = %v, torn down = %v", err, tornDown)
	}
}

// A sender on the previous release names no cluster: carried out as before.
func TestTeardownNamespaceOfCluster_withoutAClusterIDIsNotChecked(t *testing.T) {
	var tornDown bool
	s := spawnerWithState(t, "c-new", &tornDown)
	if err := s.TeardownNamespaceOfCluster(context.Background(), "acme", "", false); err != nil || !tornDown {
		t.Fatalf("err = %v, torn down = %v", err, tornDown)
	}
}

func TestTeardownNamespaceOfCluster_anUnreadableStateRefuses(t *testing.T) {
	var tornDown bool
	s := spawnerWithState(t, "c1", &tornDown)
	if err := os.WriteFile(filepath.Join(s.namespaceBase, "acme", "cluster-state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := s.TeardownNamespaceOfCluster(context.Background(), "acme", "c1", false)
	if err == nil || errors.Is(err, ErrClusterMismatch) || tornDown {
		t.Fatalf("err = %v, torn down = %v: a node that cannot say whose namespace it holds must not tear it down", err, tornDown)
	}
}

func TestTeardownSFUAndTURNOfCluster_refuseAnotherClustersNamespace(t *testing.T) {
	var tornDown bool
	s := spawnerWithState(t, "c-new", &tornDown)
	s.teardownServiceFn = func(string, systemd.ServiceType) error { tornDown = true; return nil }
	if err := s.TeardownSFUOfCluster(context.Background(), "acme", "n1", "c-old"); !errors.Is(err, ErrClusterMismatch) {
		t.Fatalf("SFU: err = %v", err)
	}
	if err := s.TeardownTURNOfCluster(context.Background(), "acme", "n1", "c-old"); !errors.Is(err, ErrClusterMismatch) {
		t.Fatalf("TURN: err = %v", err)
	}
	if tornDown {
		t.Fatal("a unit of the new namespace was torn down")
	}
}
