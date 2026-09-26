package namespace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// turnHostCM is a ClusterManager on a node holding one TURN allocation for
// namespace "acme", with a systemd spawner that is never asked to run
// anything before the config is written.
func turnHostCM(t *testing.T, allocErr error) *ClusterManager {
	t.Helper()
	nsBase := t.TempDir()
	state := ClusterLocalState{ClusterID: "cluster-acme", NamespaceName: "acme"}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(nsBase, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nsBase, "acme", "cluster-state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	db := &recoveryMockDB{}
	db.queryFunc = func(dest any, query string, _ ...any) error {
		switch {
		case strings.Contains(query, "FROM namespace_clusters"):
			appendToSlice(dest, map[string]any{"ID": "cluster-acme", "NamespaceName": "acme"})
		case strings.Contains(query, "FROM webrtc_port_allocations"):
			if allocErr != nil {
				return allocErr
			}
			appendToSlice(dest, map[string]any{
				"TURNListenPort": 3478, "TURNTLSPort": 5349,
				"TURNRelayPortStart": 49152, "TURNRelayPortEnd": 49951,
			})
		case strings.Contains(query, "namespace_webrtc_config"):
			appendToSlice(dest, map[string]any{"NamespaceName": "acme", "Enabled": true, "TURNSharedSecret": "acme-secret"})
		case strings.Contains(query, "dns_nodes"):
			appendToSlice(dest, map[string]any{"IP": "203.0.113.7"})
		}
		return nil
	}
	logger := zap.NewNop()
	spawner := NewSystemdSpawner(nsBase, "", logger)
	spawner.caddyStorageDirOverride = t.TempDir() // no wildcard cert: TURNS stays off
	spawner.systemdMgr = systemd.NewManager(nsBase, logger)
	return &ClusterManager{
		db:                  db,
		logger:              logger,
		localNodeID:         "node-1",
		baseDomain:          "orama-devnet.network",
		baseDataDir:         nsBase,
		systemdSpawner:      spawner,
		webrtcPortAllocator: NewWebRTCPortAllocator(db, logger),
	}
}

// The shared TURN config used to live in configs/, read-only to the index
// gateway that writes it. Every write failed, ReconcileHostTURN logged it and
// returned nil, and no caller ever learned host TURN was not configured.
func TestReconcileHostTURN_returnsAConfigWriteFailure(t *testing.T) {
	cm := turnHostCM(t, nil)
	blocker := filepath.Join(t.TempDir(), "read-only-configs")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := swapHostTURNConfigPath(t, filepath.Join(blocker, "turn.yaml"))
	defer restore()

	served, err := cm.ReconcileHostTURN(context.Background())
	if err == nil {
		t.Fatal("a TURN config that could not be written was reported as reconciled")
	}
	if !strings.Contains(err.Error(), "shared TURN config") {
		t.Errorf("the error does not say what failed: %v", err)
	}
	if len(served) != 0 {
		t.Errorf("namespaces %v were reported as served by a config that was never written", served)
	}
}

// An unreadable control plane must not be read as "this node serves nobody",
// and must not be silent either.
func TestReconcileHostTURN_returnsAnAllocationLookupFailure(t *testing.T) {
	cm := turnHostCM(t, errors.New("rqlite: no leader"))
	restore := swapHostTURNConfigPath(t, filepath.Join(t.TempDir(), "turn.yaml"))
	defer restore()

	if _, err := cm.ReconcileHostTURN(context.Background()); err == nil {
		t.Fatal("an allocation lookup failure was swallowed")
	}
}

// A node that holds no TURN allocation must remove the config, which carries
// every tenant's HMAC secret — and say so when it cannot.
func TestRemoveHostTURNConfig_returnsARemoveFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "turn")
	path := filepath.Join(dir, "turn.yaml")
	if err := os.MkdirAll(path, 0o700); err != nil { // a non-empty directory cannot be removed as a file
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := swapHostTURNConfigPath(t, path)
	defer restore()

	if err := removeHostTURNConfig(); err == nil {
		t.Fatal("a config that could not be removed was reported gone")
	}
}

func TestRemoveHostTURNConfig_absentIsNotAnError(t *testing.T) {
	restore := swapHostTURNConfigPath(t, filepath.Join(t.TempDir(), "turn.yaml"))
	defer restore()
	if err := removeHostTURNConfig(); err != nil {
		t.Fatalf("an already-absent config: %v", err)
	}
}

// The shared config lives where orama-turn.service reads it, under data/ —
// the tree the index gateway may write.
func TestHostTURNConfigPath_isUnderTheWritableDataTree(t *testing.T) {
	if want := "/opt/orama/.orama/data/turn/turn.yaml"; hostTURNConfigPath != want {
		t.Errorf("hostTURNConfigPath = %q, want %q (orama-turn.service TURN_CONFIG)", hostTURNConfigPath, want)
	}
}
