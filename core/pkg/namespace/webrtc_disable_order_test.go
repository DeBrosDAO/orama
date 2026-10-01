package namespace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// trackWrites records, in the rig's event log, the registry writes a disable
// makes that the order and the retention of allocations depend on:
// "db:config-disabled", "db:dealloc-all" and "db:dealloc:<node>:<service>".
func trackWrites(r *disableRig) {
	inner := r.db.execFunc
	r.db.execFunc = func(query string, args ...any) error {
		switch {
		case strings.Contains(query, "UPDATE namespace_webrtc_config SET enabled = 0"):
			r.record("db:config-disabled")
		case strings.Contains(query, "DELETE FROM webrtc_port_allocations WHERE namespace_cluster_id = ? AND node_id = ?"):
			r.record(fmt.Sprintf("db:dealloc:%v:%v", args[1], args[2]))
		case strings.Contains(query, "DELETE FROM webrtc_port_allocations"):
			r.record("db:dealloc-all")
		}
		return inner(query, args...)
	}
}

// The bug: DisableWebRTC stopped the SFU while the registry still said WebRTC
// was enabled, so a node's reconciler started the draining unit again and the
// teardown failed with "Job canceled". The registry is told first.
func TestDisableWebRTC_marksTheConfigDisabledBeforeStoppingAnyUnit(t *testing.T) {
	r := newDisableRig(t)
	trackWrites(r)

	if err := r.cm.DisableWebRTC(context.Background(), "acme"); err != nil {
		t.Fatalf("DisableWebRTC: %v", err)
	}

	marked := r.index("db:config-disabled")
	if marked < 0 {
		t.Fatalf("the config was never marked disabled: %v", r.events)
	}
	for _, prefix := range []string{"local-teardown:", "remote:teardown-"} {
		if first := r.index(prefix); first >= 0 && first < marked {
			t.Errorf("a unit was stopped (%q at %d) before the config was marked disabled (%d): %v", prefix, first, marked, r.events)
		}
	}
}

// A disable that cannot record the change must not have stopped anything:
// units stopped under a config that says enabled are started again.
func TestDisableWebRTC_stopsNothingWhenTheConfigCannotBeMarkedDisabled(t *testing.T) {
	r := newDisableRig(t)
	trackWrites(r)
	inner := r.db.execFunc
	r.db.execFunc = func(query string, args ...any) error {
		if strings.Contains(query, "UPDATE namespace_webrtc_config SET enabled = 0") {
			return errors.New("rqlite write failed")
		}
		return inner(query, args...)
	}

	err := r.cm.DisableWebRTC(context.Background(), "acme")
	if err == nil || !strings.Contains(err.Error(), "rqlite write failed") {
		t.Fatalf("err = %v, want the failed write reported", err)
	}
	for _, e := range r.events {
		if strings.HasPrefix(e, "local-teardown:") || strings.HasPrefix(e, "remote:") || e == "db:config-deleted" || strings.HasPrefix(e, "db:dealloc") {
			t.Errorf("%q happened although the disable was refused: %v", e, r.events)
		}
	}
}

// The other half of the bug: the failed teardown still freed the allocation, so
// the next namespace was given the ports the unit still held and its own SFU
// crash-looped on "address already in use". A unit that is not gone keeps its
// ports reserved.
func TestDisableWebRTC_keepsTheAllocationOfAUnitThatCouldNotBeStopped(t *testing.T) {
	r := newDisableRig(t)
	trackWrites(r)
	r.remoteErr = func(action string) error {
		if action == "teardown-sfu" {
			return errors.New("Job canceled")
		}
		return nil
	}

	if err := r.cm.DisableWebRTC(context.Background(), "acme"); err == nil {
		t.Fatal("a failed teardown was not reported")
	}

	if r.has("db:dealloc-all") {
		t.Errorf("every allocation was freed although node-2's SFU is still up: %v", r.events)
	}
	if r.has("db:dealloc:node-2:sfu") {
		t.Errorf("the allocation of the SFU that could not be stopped was freed: %v", r.events)
	}
	for _, want := range []string{"db:dealloc:node-1:sfu", "db:dealloc:node-1:turn", "db:dealloc:node-2:turn"} {
		if !r.has(want) {
			t.Errorf("%s: a unit that is gone kept its allocation: %v", want, r.events)
		}
	}
}

func TestDisableWebRTC_keepsTheAllocationOfALocalUnitThatCouldNotBeStopped(t *testing.T) {
	r := newDisableRig(t)
	trackWrites(r)
	r.cm.systemdSpawner.teardownServiceFn = func(_ string, svc systemd.ServiceType) error {
		if svc == systemd.ServiceTypeSFU {
			return errors.New("sfu was still deactivating after 90s")
		}
		return nil
	}

	if err := r.cm.DisableWebRTC(context.Background(), "acme"); err == nil {
		t.Fatal("a failed teardown was not reported")
	}
	if r.has("db:dealloc-all") || r.has("db:dealloc:node-1:sfu") {
		t.Errorf("the allocation of the local SFU was freed while it is still up: %v", r.events)
	}
	if !r.has("db:dealloc:node-2:sfu") {
		t.Errorf("the remote SFU, which was torn down, kept its allocation: %v", r.events)
	}
}

func TestDisableWebRTC_freesEveryAllocationWhenEveryUnitIsGone(t *testing.T) {
	r := newDisableRig(t)
	trackWrites(r)

	if err := r.cm.DisableWebRTC(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
	if !r.has("db:dealloc-all") {
		t.Errorf("the allocations were not freed: %v", r.events)
	}
}

// Running the disable again finishes one that left allocations behind, even
// though the first run already marked the config disabled.
func TestDisableWebRTC_canBeRunAgainToFinishAnIncompleteDisable(t *testing.T) {
	t.Run("allocations remain", func(t *testing.T) {
		r := newDisableRig(t)
		inner := r.db.queryFunc
		r.db.queryFunc = func(dest any, query string, args ...any) error {
			if strings.Contains(query, "FROM namespace_webrtc_config") {
				return nil // disabled by the first run
			}
			return inner(dest, query, args...)
		}
		if err := r.cm.DisableWebRTC(context.Background(), "acme"); err != nil {
			t.Fatalf("DisableWebRTC: %v", err)
		}
		if !r.has("local-teardown:sfu:acme") {
			t.Errorf("the retry stopped nothing: %v", r.events)
		}
	})
	t.Run("nothing remains", func(t *testing.T) {
		r := newDisableRig(t)
		inner := r.db.queryFunc
		r.db.queryFunc = func(dest any, query string, args ...any) error {
			if strings.Contains(query, "FROM namespace_webrtc_config") || strings.Contains(query, "FROM webrtc_port_allocations") {
				return nil
			}
			return inner(dest, query, args...)
		}
		if err := r.cm.DisableWebRTC(context.Background(), "acme"); !errors.Is(err, ErrWebRTCNotEnabled) {
			t.Fatalf("err = %v, want ErrWebRTCNotEnabled", err)
		}
	})
}

func TestDisableWebRTC_aFailedConfigReadIsNotReportedAsNotEnabled(t *testing.T) {
	r := newDisableRig(t)
	inner := r.db.queryFunc
	r.db.queryFunc = func(dest any, query string, args ...any) error {
		if strings.Contains(query, "FROM namespace_webrtc_config") {
			return errors.New("rqlite unavailable")
		}
		return inner(dest, query, args...)
	}
	err := r.cm.DisableWebRTC(context.Background(), "acme")
	if err == nil || errors.Is(err, ErrWebRTCNotEnabled) || !strings.Contains(err.Error(), "rqlite unavailable") {
		t.Fatalf("err = %v, want the read failure", err)
	}
}

// A rolled-back enablement follows the same rules: disabled before anything is
// stopped, and no allocation freed under a unit that is still up.
func TestCleanupWebRTCOnError_marksDisabledFirstAndKeepsAnAllocationItCouldNotRelease(t *testing.T) {
	r := newDisableRig(t)
	trackWrites(r)
	r.remoteErr = func(action string) error {
		if action == "teardown-sfu" {
			return errors.New("node unreachable")
		}
		return nil
	}

	r.cm.cleanupWebRTCOnError(context.Background(), "cluster-acme", "acme", []clusterNodeInfo{
		{NodeID: "node-1", InternalIP: "10.0.0.1"},
		{NodeID: "node-2", InternalIP: "10.0.0.2"},
	})

	if marked, first := r.index("db:config-disabled"), r.index("local-teardown:"); marked < 0 || first < marked {
		t.Errorf("the config was not marked disabled before the first stop (%d, %d): %v", marked, first, r.events)
	}
	if r.has("db:dealloc-all") || r.has("db:dealloc:node-2:sfu") {
		t.Errorf("the allocation of the SFU on node-2, which could not be stopped, was freed: %v", r.events)
	}
	if !r.has("db:dealloc:node-1:sfu") {
		t.Errorf("node-1's SFU is gone but kept its allocation: %v", r.events)
	}
}

// The replay of a teardown that failed first is what finally frees the ports.
func TestReplayPendingCleanups_freesTheAllocationOnceTheTeardownSucceeds(t *testing.T) {
	for _, tc := range []struct {
		action   string
		wantGone []string
	}{
		{teardownSFUAction, []string{"sfu"}},
		{teardownTURNAction, []string{"turn"}},
		{teardownAction, []string{"sfu", "turn"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			r := newRegistryRig(t)
			r.cluster("c-acme", "acme")
			r.webrtc("c-acme", "node1", "sfu")
			r.webrtc("c-acme", "node1", "turn")
			r.webrtc("c-acme", "node2", "sfu")
			r.pending("acme", "node1", tc.action, "c-acme", false)

			if err := r.replay(); err != nil {
				t.Fatal(err)
			}

			left := map[string]bool{}
			rows, err := r.db.Query(`SELECT node_id, service_type FROM webrtc_port_allocations`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var node, svc string
				if err := rows.Scan(&node, &svc); err != nil {
					t.Fatal(err)
				}
				left[node+":"+svc] = true
			}
			for _, svc := range tc.wantGone {
				if left["node1:"+svc] {
					t.Errorf("node1's %s allocation survived its completed teardown", svc)
				}
			}
			if !left["node2:sfu"] {
				t.Error("an allocation on another node was freed")
			}
		})
	}
}

func TestReplayPendingCleanups_keepsTheAllocationWhileTheTeardownStillFails(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-acme", "acme")
	r.webrtc("c-acme", "node1", "sfu")
	r.pending("acme", "node1", teardownSFUAction, "c-acme", false)
	r.sendErr = errors.New("node unreachable")

	_ = r.replay()

	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM webrtc_port_allocations`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d (%v), want the allocation kept while the unit may still be up", n, err)
	}
}

func deprovisionRig(t *testing.T) (*registryRig, int64) {
	t.Helper()
	r := newRegistryRig(t)
	lg := zap.NewNop()
	r.cm.portAllocator = NewNamespacePortAllocator(r.client, lg)
	r.cm.dnsManager = NewDNSRecordManager(r.client, "example.test", lg)
	r.exec(`INSERT INTO dns_nodes (id, ip_address, internal_ip, status) VALUES ('node1', '192.0.2.1', '10.0.0.1', 'active')`)
	r.cluster("c1", "acme")
	r.membership("c1", "node1")
	r.webrtc("c1", "node1", "sfu")
	var nsID int64
	if err := r.db.QueryRow(`SELECT namespace_id FROM namespace_clusters WHERE id = 'c1'`).Scan(&nsID); err != nil {
		t.Fatal(err)
	}
	return r, nsID
}

func webrtcRows(t *testing.T, r *registryRig) int {
	t.Helper()
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM webrtc_port_allocations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeprovisionCluster_freesTheWebRTCAllocationsOfATornDownNamespace(t *testing.T) {
	r, nsID := deprovisionRig(t)
	if err := r.cm.DeprovisionCluster(context.Background(), nsID); err != nil {
		t.Fatal(err)
	}
	if n := webrtcRows(t, r); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

// A namespace that a node did not confirm torn down may still run its SFU
// there; its ports stay reserved until the recorded teardown is carried out.
func TestDeprovisionCluster_keepsTheWebRTCAllocationsOfANodeThatDidNotConfirmTheTeardown(t *testing.T) {
	r, nsID := deprovisionRig(t)
	r.sendErr = errors.New("node unreachable")

	if err := r.cm.DeprovisionCluster(context.Background(), nsID); err == nil {
		t.Fatal("a teardown that no node confirmed was reported as done")
	}
	if n := webrtcRows(t, r); n != 1 {
		t.Errorf("rows = %d, want the SFU's allocation kept", n)
	}

	r.sendErr = nil
	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if n := webrtcRows(t, r); n != 0 {
		t.Errorf("rows = %d after the teardown was replayed, want 0", n)
	}
}

// A pending cleanup recorded before cluster_id existed names no rows to free.
func TestReplayPendingCleanups_aCleanupWithoutAClusterIDFreesNothing(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-acme", "acme")
	r.webrtc("c-acme", "node1", "sfu")
	r.pending("acme", "node1", teardownSFUAction, "", false)

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM webrtc_port_allocations`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d (%v), want 1", n, err)
	}
}
