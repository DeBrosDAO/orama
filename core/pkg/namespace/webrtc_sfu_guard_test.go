package namespace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// sfuRig is the registry schema with a ClusterManager whose node is
// "coordinator" and which has a spawner over a temp directory.
type sfuRig struct {
	*registryRig
	nsBase string
}

func newSFURig(t *testing.T) *sfuRig {
	t.Helper()
	r := &sfuRig{registryRig: newRegistryRig(t), nsBase: t.TempDir()}
	r.cm.systemdSpawner = NewSystemdSpawner(r.nsBase, "", zap.NewNop())
	return r
}

// sfuConfig writes the config SpawnSFU leaves for this node's SFU of acme.
func (r *sfuRig) sfuConfig(listen string, mediaStart, mediaEnd int) {
	r.t.Helper()
	dir := filepath.Join(r.nsBase, "acme", "configs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	body := fmt.Sprintf("listen_addr: %s\nnamespace: acme\nmedia_port_start: %d\nmedia_port_end: %d\n", listen, mediaStart, mediaEnd)
	if err := os.WriteFile(filepath.Join(dir, "sfu-coordinator.yaml"), []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *sfuRig) webrtcConfig(cluster, namespace string, enabled bool) {
	r.exec(`INSERT INTO namespace_webrtc_config (id, namespace_cluster_id, namespace_name, enabled, turn_shared_secret, enabled_by)
		VALUES (?, ?, ?, ?, 'secret', 'test')`, fmt.Sprint("cfg", r.id()), cluster, namespace, enabled)
}

func (r *sfuRig) sfuRows(cluster string) int {
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM webrtc_port_allocations WHERE namespace_cluster_id = ? AND service_type = 'sfu'`, cluster).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

// The bug's other half: a unit running without an allocation row had its ports
// handed to the next namespace. The registry now learns what the unit holds.
func TestBackfillSFUAllocation_recordsTheRunningUnitsPorts(t *testing.T) {
	r := newSFURig(t)
	r.cluster("c-acme", "acme")
	r.membership("c-acme", "coordinator")
	r.sfuConfig("10.0.0.1:30000", 20000, 20499)

	if err := r.cm.backfillSFUAllocation(context.Background(), "acme", "c-acme"); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	blk, err := r.cm.webrtcPortAllocator.GetSFUPorts(context.Background(), "c-acme", "coordinator")
	if err != nil || blk == nil {
		t.Fatalf("no row recorded (%v)", err)
	}
	if blk.SFUSignalingPort != 30000 || blk.SFUMediaPortStart != 20000 || blk.SFUMediaPortEnd != 20499 {
		t.Errorf("recorded %+v, want 30000 / 20000-20499", blk)
	}

	next, err := r.cm.webrtcPortAllocator.AllocateSFUPorts(context.Background(), "coordinator", "c-other")
	if err != nil {
		t.Fatal(err)
	}
	if next.SFUSignalingPort == 30000 || next.SFUMediaPortStart == 20000 {
		t.Errorf("the next namespace was given the recorded ports: %+v", next)
	}
}

func TestBackfillSFUAllocation_isIdempotent(t *testing.T) {
	r := newSFURig(t)
	r.cluster("c-acme", "acme")
	r.membership("c-acme", "coordinator")
	r.sfuConfig("10.0.0.1:30000", 20000, 20499)

	for i := 0; i < 3; i++ {
		if err := r.cm.backfillSFUAllocation(context.Background(), "acme", "c-acme"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if n := r.sfuRows("c-acme"); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

func TestBackfillSFUAllocation_recordsNothingWithoutAUnitOrMembership(t *testing.T) {
	t.Run("no SFU config on this node", func(t *testing.T) {
		r := newSFURig(t)
		r.cluster("c-acme", "acme")
		r.membership("c-acme", "coordinator")
		if err := r.cm.backfillSFUAllocation(context.Background(), "acme", "c-acme"); err != nil {
			t.Fatal(err)
		}
		if n := r.sfuRows("c-acme"); n != 0 {
			t.Errorf("rows = %d, want 0", n)
		}
	})
	t.Run("this node is no longer a member", func(t *testing.T) {
		r := newSFURig(t)
		r.cluster("c-acme", "acme")
		r.membership("c-acme", "some-other-node")
		r.sfuConfig("10.0.0.1:30000", 20000, 20499)
		if err := r.cm.backfillSFUAllocation(context.Background(), "acme", "c-acme"); err != nil {
			t.Fatal(err)
		}
		if n := r.sfuRows("c-acme"); n != 0 {
			t.Errorf("a stale config of a departed member was recorded: rows = %d", n)
		}
	})
	t.Run("unknown identity", func(t *testing.T) {
		r := newSFURig(t)
		r.cm.localNodeID = ""
		if err := r.cm.backfillSFUAllocation(context.Background(), "acme", "c-acme"); err != nil {
			t.Fatal(err)
		}
	})
}

// A config that does not describe a block the allocator could have handed out
// is not recorded: the unique index could not tell it from its neighbours.
func TestBackfillSFUAllocation_refusesAConfigOffTheAllocatorsGrid(t *testing.T) {
	for name, tc := range map[string]struct {
		listen     string
		start, end int
	}{
		"media range straddles two blocks": {"10.0.0.1:30000", 20100, 20599},
		"media range of the wrong size":    {"10.0.0.1:30000", 20000, 20999},
		"media range outside the range":    {"10.0.0.1:30000", 30000, 30499},
		"signaling port outside the range": {"10.0.0.1:8080", 20000, 20499},
		"no port in listen_addr":           {"10.0.0.1", 20000, 20499},
	} {
		t.Run(name, func(t *testing.T) {
			r := newSFURig(t)
			r.cluster("c-acme", "acme")
			r.membership("c-acme", "coordinator")
			r.sfuConfig(tc.listen, tc.start, tc.end)
			if err := r.cm.backfillSFUAllocation(context.Background(), "acme", "c-acme"); err == nil {
				t.Fatal("an off-grid config was accepted")
			}
			if n := r.sfuRows("c-acme"); n != 0 {
				t.Errorf("rows = %d, want 0", n)
			}
		})
	}
}

// The unit and the registry disagree: another cluster holds the ports. The
// caller is told, so it does not stop the unit as "unallocated".
func TestBackfillSFUAllocation_reportsAConflictWithAnotherCluster(t *testing.T) {
	r := newSFURig(t)
	r.cluster("c-acme", "acme")
	r.membership("c-acme", "coordinator")
	r.sfuConfig("10.0.0.1:30000", 20000, 20499)
	if _, err := r.cm.webrtcPortAllocator.RecordSFUPorts(context.Background(), "coordinator", "c-new", 30000, 20000, 20499); err != nil {
		t.Fatal(err)
	}

	err := r.cm.backfillSFUAllocation(context.Background(), "acme", "c-acme")
	if err == nil || !isConflictError(err) {
		t.Fatalf("err = %v, want the conflict reported", err)
	}
}

// spawnRig is an sfuRig with acme's WebRTC enabled and its SFU allocated on
// this node, and a spawner that cannot actually start anything: a spawn the
// guard lets through fails on the missing rqlite password and records a backoff,
// which is how a test sees that one was attempted.
func newSpawnRig(t *testing.T) (*sfuRig, *ClusterLocalState) {
	t.Helper()
	r := newSFURig(t)
	r.cluster("c-acme", "acme")
	r.membership("c-acme", "coordinator")
	r.webrtcConfig("c-acme", "acme", true)
	if _, err := r.cm.webrtcPortAllocator.RecordSFUPorts(context.Background(), "coordinator", "c-acme", 30000, 20000, 20499); err != nil {
		t.Fatal(err)
	}
	state := &ClusterLocalState{ClusterID: "c-acme", NamespaceName: "acme", LocalIP: "10.0.0.1"}
	state.LocalPorts.RQLiteHTTPPort = 10001
	return r, state
}

func presentUnitState(t *testing.T, state systemd.ActiveState, err error) {
	t.Helper()
	original := serviceActiveState
	serviceActiveState = func(*systemd.Manager, string, systemd.ServiceType) (systemd.ActiveState, error) {
		return state, err
	}
	t.Cleanup(func() { serviceActiveState = original })
}

// The bug: DisableWebRTC's stop leaves the SFU deactivating for up to 45s, and
// the reconciler read "not active" as "down" and started it, cancelling the
// stop. Only a stopped unit is started.
func TestSpawnAllocatedWebRTCServices_onlyStartsAStoppedUnit(t *testing.T) {
	for _, tc := range []struct {
		state      systemd.ActiveState
		wantSpawn  bool
		whatItIs   string
		stateError error
	}{
		{state: systemd.ActiveStateInactive, wantSpawn: true, whatItIs: "stopped"},
		{state: systemd.ActiveStateFailed, wantSpawn: true, whatItIs: "failed"},
		{state: systemd.ActiveStateActive, whatItIs: "running"},
		{state: systemd.ActiveStateDeactivating, whatItIs: "being stopped"},
		{state: systemd.ActiveStateActivating, whatItIs: "starting"},
		{state: systemd.ActiveStateReloading, whatItIs: "reloading"},
		{stateError: errors.New("systemctl show failed"), whatItIs: "of unknown state"},
	} {
		t.Run(tc.whatItIs, func(t *testing.T) {
			r, state := newSpawnRig(t)
			presentUnitState(t, tc.state, tc.stateError)

			r.cm.spawnAllocatedWebRTCServices(context.Background(), state)

			if got := r.cm.spawnBackoffActive("acme"); got != tc.wantSpawn {
				t.Errorf("a spawn was attempted = %v for a unit that is %s, want %v", got, tc.whatItIs, tc.wantSpawn)
			}
		})
	}
}

// Disable marks the config disabled before stopping; a sweep that read the
// config earlier must not start the unit afterwards.
func TestSpawnAllocatedWebRTCServices_doesNotStartADisabledNamespace(t *testing.T) {
	r, state := newSpawnRig(t)
	r.exec(`UPDATE namespace_webrtc_config SET enabled = 0 WHERE namespace_cluster_id = 'c-acme'`)
	presentUnitState(t, systemd.ActiveStateInactive, nil)

	r.cm.spawnAllocatedWebRTCServices(context.Background(), state)

	if r.cm.spawnBackoffActive("acme") {
		t.Error("the SFU of a namespace whose WebRTC is disabled was started")
	}
}

// A teardown holds the namespace's lock from the stop to the removal of the
// config; a start decided before it began waits for it.
func TestSpawnAllocatedWebRTCServices_waitsForATeardownOfTheNamespace(t *testing.T) {
	r, state := newSpawnRig(t)
	presentUnitState(t, systemd.ActiveStateInactive, nil)

	unlock := mustLockNamespace(t, r.cm.systemdSpawner, "acme")
	done := make(chan struct{})
	go func() {
		r.cm.spawnAllocatedWebRTCServices(context.Background(), state)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("the reconciler's start ran while a teardown held the namespace's lock")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the start never ran after the lock was released")
	}
}

func TestTeardownSFU_holdsTheNamespaceLock(t *testing.T) {
	s := NewSystemdSpawner(t.TempDir(), "", zap.NewNop())
	entered := make(chan struct{})
	release := make(chan struct{})
	s.teardownServiceFn = func(string, systemd.ServiceType) error {
		close(entered)
		<-release
		return nil
	}
	finished := make(chan error, 1)
	go func() { finished <- s.TeardownSFU(context.Background(), "acme", "node-1") }()
	<-entered

	got := make(chan struct{})
	go func() {
		mustLockNamespace(t, s, "acme")()
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("the namespace lock was free during the SFU teardown")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was never released")
	}
}
