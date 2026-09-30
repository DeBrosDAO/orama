package namespace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// disableRig is a ClusterManager for namespace "acme" with WebRTC enabled on
// the local node-1 (SFU + TURN) and on remote node-2 (SFU + TURN). Everything a
// disable does to a unit is recorded, in order, in events.
type disableRig struct {
	cm     *ClusterManager
	db     *recoveryMockDB
	nsBase string

	mu     sync.Mutex
	events []string
	// remoteErr fails a remote spawn request for the action it returns an error for.
	remoteErr func(action string) error
}

func (r *disableRig) record(e string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *disableRig) has(e string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, got := range r.events {
		if got == e {
			return true
		}
	}
	return false
}

func (r *disableRig) index(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, got := range r.events {
		if strings.HasPrefix(got, prefix) {
			return i
		}
	}
	return -1
}

func newDisableRig(t *testing.T) *disableRig {
	t.Helper()
	r := &disableRig{nsBase: t.TempDir()}
	logger := zap.NewNop()

	r.db = &recoveryMockDB{}
	r.db.queryFunc = func(dest any, query string, _ ...any) error {
		switch {
		case strings.Contains(query, "FROM namespace_clusters"):
			appendToSlice(dest, map[string]any{"ID": "cluster-acme", "NamespaceName": "acme"})
		case strings.Contains(query, "FROM namespace_webrtc_config"):
			appendToSlice(dest, map[string]any{"NamespaceName": "acme", "Enabled": true})
		case strings.Contains(query, "FROM namespace_cluster_nodes"):
			appendToSlice(dest, map[string]any{"NodeID": "node-1", "InternalIP": "10.0.0.1", "PublicIP": "203.0.113.1"})
			appendToSlice(dest, map[string]any{"NodeID": "node-2", "InternalIP": "10.0.0.2", "PublicIP": "203.0.113.2"})
		case strings.Contains(query, "FROM webrtc_port_allocations"):
			for _, n := range []string{"node-1", "node-2"} {
				appendToSlice(dest, map[string]any{"NodeID": n, "ServiceType": "turn"})
				appendToSlice(dest, map[string]any{"NodeID": n, "ServiceType": "sfu"})
			}
		}
		return nil
	}
	r.db.execFunc = func(query string, _ ...any) error {
		switch {
		case strings.Contains(query, "DELETE FROM namespace_webrtc_config"):
			r.record("db:config-deleted")
		case strings.Contains(query, "INSERT INTO namespace_pending_cleanup"):
			r.record("db:pending-cleanup")
		}
		return nil
	}

	spawner := NewSystemdSpawner(r.nsBase, "", logger)
	spawner.teardownServiceFn = func(ns string, svc systemd.ServiceType) error {
		r.record("local-teardown:" + string(svc) + ":" + ns)
		return nil
	}
	r.cm = &ClusterManager{
		db:                  r.db,
		logger:              logger,
		localNodeID:         "node-1",
		baseDataDir:         r.nsBase,
		systemdSpawner:      spawner,
		webrtcPortAllocator: NewWebRTCPortAllocator(r.db, logger),
		portAllocator:       NewNamespacePortAllocator(r.db, logger),
		dnsManager:          NewDNSRecordManager(r.db, "example.test", logger),
		spawnRequestFn: func(_ context.Context, ip string, req map[string]interface{}) (*spawnResponse, error) {
			action, _ := req["action"].(string)
			r.record("remote:" + action + ":" + ip)
			if r.remoteErr != nil {
				if err := r.remoteErr(action); err != nil {
					return nil, err
				}
			}
			return &spawnResponse{Success: true}, nil
		},
		reconcileHostTURNFn: func(context.Context) ([]string, error) {
			r.record("reconcile-host-turn")
			return nil, nil
		},
	}
	return r
}

// Regression: disabling WebRTC used to send stop-sfu/stop-turn, which stop a
// unit and leave it ENABLED with its env file — the next `orama node upgrade`
// restarted SFU for a namespace that had turned WebRTC off. The SFU is now
// torn down (stopped, disabled, env removed) on every node.
func TestDisableWebRTC_tearsDownTheSFUOnEveryNode(t *testing.T) {
	r := newDisableRig(t)

	if err := r.cm.DisableWebRTC(context.Background(), "acme"); err != nil {
		t.Fatalf("DisableWebRTC: %v", err)
	}

	if !r.has("local-teardown:sfu:acme") {
		t.Errorf("the local SFU was not torn down: %v", r.events)
	}
	if !r.has("remote:teardown-sfu:10.0.0.2") {
		t.Errorf("the remote SFU was not torn down: %v", r.events)
	}
	for _, e := range r.events {
		if strings.HasPrefix(e, "remote:stop-") {
			t.Errorf("a stop-only action %q was sent: it leaves the unit enabled for the next upgrade", e)
		}
	}
}

// TURN is one shared host server. Disabling WebRTC for one namespace must not
// stop or disable it (other namespaces relay through it): only the legacy
// per-namespace unit is torn down, and the namespace leaves the shared tenant
// list through ReconcileHostTURN once its allocation and config are gone.
func TestDisableWebRTC_leavesTheSharedTURNAndDropsTheTenant(t *testing.T) {
	r := newDisableRig(t)

	if err := r.cm.DisableWebRTC(context.Background(), "acme"); err != nil {
		t.Fatalf("DisableWebRTC: %v", err)
	}

	for _, e := range r.events {
		if strings.Contains(e, "orama-turn.service") || e == "remote:stop-turn:10.0.0.2" {
			t.Errorf("the shared TURN was stopped: %q", e)
		}
	}
	if !r.has("remote:teardown-turn:10.0.0.2") || !r.has("local-teardown:turn:acme") {
		t.Errorf("the legacy per-namespace TURN unit was not retired: %v", r.events)
	}
	rec, del := r.index("reconcile-host-turn"), r.index("db:config-deleted")
	if rec < 0 || del < 0 || rec < del {
		t.Errorf("the host TURN must be reconciled after the WebRTC config is deleted (config=%d reconcile=%d): %v", del, rec, r.events)
	}
}

// A node that cannot be reached is not silently skipped: the failure is
// recorded for retry and returned, while the disable still completes.
func TestDisableWebRTC_reportsAndRecordsAnUnreachableNode(t *testing.T) {
	r := newDisableRig(t)
	r.remoteErr = func(action string) error {
		if action == "teardown-sfu" {
			return errors.New("unknown action: teardown-sfu") // a node on the previous release
		}
		return nil
	}

	err := r.cm.DisableWebRTC(context.Background(), "acme")
	if err == nil || !strings.Contains(err.Error(), "node-2") || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("err = %v, want the node-2 SFU teardown failure", err)
	}
	if !r.has("db:pending-cleanup") {
		t.Errorf("the failed remote teardown was not recorded for retry: %v", r.events)
	}
	if !r.has("db:config-deleted") {
		t.Errorf("the disable must still complete: %v", r.events)
	}
}

func TestDisableWebRTC_reportsALocalTeardownFailure(t *testing.T) {
	r := newDisableRig(t)
	r.cm.systemdSpawner.teardownServiceFn = func(string, systemd.ServiceType) error {
		return errors.New("disable failed")
	}

	err := r.cm.DisableWebRTC(context.Background(), "acme")
	if err == nil || !strings.Contains(err.Error(), "disable failed") {
		t.Fatalf("err = %v, want the local teardown failure", err)
	}
}

func TestDisableWebRTC_reportsASharedTURNReconcileFailure(t *testing.T) {
	r := newDisableRig(t)
	r.cm.reconcileHostTURNFn = func(context.Context) ([]string, error) { return nil, errors.New("config write failed") }

	err := r.cm.DisableWebRTC(context.Background(), "acme")
	if err == nil || !strings.Contains(err.Error(), "config write failed") {
		t.Fatalf("err = %v, want the shared TURN reconcile failure", err)
	}
}

func TestTeardownSFU_removesTheConfigAndKeepsItWhenTheUnitCannotBeTornDown(t *testing.T) {
	nsBase := t.TempDir()
	cfg := filepath.Join(nsBase, "acme", "configs", "sfu-node-1.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("turn_secret: x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewSystemdSpawner(nsBase, "", zap.NewNop())

	s.teardownServiceFn = func(string, systemd.ServiceType) error { return errors.New("stop failed") }
	if err := s.TeardownSFU(context.Background(), "acme", "node-1"); err == nil {
		t.Fatal("a failed unit teardown was not reported")
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Fatalf("the SFU config was removed although the unit could not be torn down: %v", err)
	}

	s.teardownServiceFn = func(string, systemd.ServiceType) error { return nil }
	if err := s.TeardownSFU(context.Background(), "acme", "node-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatalf("the SFU config (it carries the TURN secret) survived: %v", err)
	}
	// Idempotent: a replayed teardown on a node that already did it succeeds.
	if err := s.TeardownSFU(context.Background(), "acme", "node-1"); err != nil {
		t.Fatalf("second teardown: %v", err)
	}
}

func TestTeardownWebRTC_refusesPlatformNamespaces(t *testing.T) {
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())
	s.teardownServiceFn = func(string, systemd.ServiceType) error { t.Fatal("torn down"); return nil }
	for _, ns := range []string{"index", "", "nameserver"} {
		if err := s.TeardownSFU(context.Background(), ns, "n"); err == nil {
			t.Errorf("TeardownSFU(%q) was allowed", ns)
		}
		if err := s.TeardownTURN(context.Background(), ns, "n"); err == nil {
			t.Errorf("TeardownTURN(%q) was allowed", ns)
		}
	}
}

// T8: the registry writes of a disable are not fire-and-forget. A config row
// that could not be deleted leaves WebRTC looking enabled; the caller is told.
func TestDisableWebRTC_reportsAFailedRegistryCleanup(t *testing.T) {
	for _, tc := range []struct{ name, statement string }{
		{"webrtc_rooms delete", "DELETE FROM webrtc_rooms"},
		{"config delete", "DELETE FROM namespace_webrtc_config"},
		{"port deallocation", "DELETE FROM webrtc_port_allocations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newDisableRig(t)
			inner := r.db.execFunc
			r.db.execFunc = func(query string, args ...any) error {
				if strings.Contains(query, tc.statement) {
					return errors.New("rqlite write failed")
				}
				return inner(query, args...)
			}

			err := r.cm.DisableWebRTC(context.Background(), "acme")
			if err == nil || !strings.Contains(err.Error(), "rqlite write failed") {
				t.Fatalf("err = %v, want the failed cleanup reported", err)
			}
		})
	}
}
