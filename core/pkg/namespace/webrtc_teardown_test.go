package namespace

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/systemd"
)

// meetingTimeout bounds how long a unit waits for the other node's teardown to
// be in flight; a sequential teardown never gets there.
const meetingTimeout = 3 * time.Second

// The bug: a disable tore the nodes down one after another, so its duration was
// the sum of every SFU drain (117s on three nodes) and outlasted the gateway's
// 120s WriteTimeout. Here the local SFU teardown cannot return until the remote
// node's has begun, and vice versa: only a concurrent teardown finishes.
func TestDisableWebRTC_tearsNodesDownConcurrently(t *testing.T) {
	r := newDisableRig(t)
	localStarted := make(chan struct{})
	remoteStarted := make(chan struct{})

	r.cm.systemdSpawner.teardownServiceFn = func(ns string, svc systemd.ServiceType) error {
		r.record("local-teardown:" + string(svc) + ":" + ns)
		if svc != systemd.ServiceTypeSFU {
			return nil
		}
		close(localStarted)
		select {
		case <-remoteStarted:
			return nil
		case <-time.After(meetingTimeout):
			return errors.New("the remote SFU teardown never began while the local one was running")
		}
	}
	inner := r.cm.spawnRequestFn
	r.cm.spawnRequestFn = func(ctx context.Context, ip string, req map[string]interface{}) (*spawnResponse, error) {
		if req["action"] == teardownSFUAction {
			close(remoteStarted)
			select {
			case <-localStarted:
			case <-time.After(meetingTimeout):
				return nil, errors.New("the local SFU teardown never began while the remote one was running")
			}
		}
		return inner(ctx, ip, req)
	}

	start := time.Now()
	if err := r.cm.DisableWebRTC(context.Background(), "acme"); err != nil {
		t.Fatalf("DisableWebRTC: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= meetingTimeout {
		t.Errorf("the disable took %v: the nodes were torn down one after another", elapsed)
	}
}

// A unit that did not stop keeps its allocation even though the other nodes
// are torn down in the same breath: a failure must neither cancel the others
// nor be forgotten.
func TestDisableWebRTC_concurrentTeardownKeepsTheAllocationOfAUnitThatDidNotStop(t *testing.T) {
	r := newDisableRig(t)
	trackWrites(r)
	r.remoteErr = func(action string) error {
		if action == teardownSFUAction {
			return errors.New("systemd refused the stop")
		}
		return nil
	}

	err := r.cm.DisableWebRTC(context.Background(), "acme")
	if err == nil || !strings.Contains(err.Error(), "systemd refused the stop") {
		t.Fatalf("err = %v, want the failed remote SFU teardown reported", err)
	}
	if !r.has("local-teardown:sfu:acme") {
		t.Errorf("the healthy local SFU was not torn down: %v", r.events)
	}
	if r.has("db:dealloc:node-2:sfu") {
		t.Errorf("the allocation of the SFU that did not stop was freed: %v", r.events)
	}
	if !r.has("db:dealloc:node-1:sfu") {
		t.Errorf("the allocation of the SFU that stopped was kept: %v", r.events)
	}
}

func TestTeardownWebRTCConcurrently_returnsOneResultPerTaskInOrder(t *testing.T) {
	r := newDisableRig(t)
	r.remoteErr = func(action string) error {
		if action == teardownTURNAction {
			return errors.New("unreachable")
		}
		return nil
	}
	tasks := []webrtcTeardownTask{
		{NodeID: "node-2", NodeIP: "10.0.0.2", ServiceType: "turn", Action: teardownTURNAction},
		{NodeID: "node-1", NodeIP: "10.0.0.1", ServiceType: "sfu", Action: teardownSFUAction},
		{NodeID: "node-2", NodeIP: "10.0.0.2", ServiceType: "sfu", Action: teardownSFUAction},
	}

	results := r.cm.teardownWebRTCConcurrently(context.Background(), "acme", "cluster-acme", tasks)

	if len(results) != len(tasks) {
		t.Fatalf("got %d results for %d tasks", len(results), len(tasks))
	}
	for i, res := range results {
		if res.Task != tasks[i] {
			t.Errorf("result %d is for %+v, want %+v", i, res.Task, tasks[i])
		}
	}
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "TURN on node node-2") {
		t.Errorf("results[0].Err = %v, want the TURN failure naming its node", results[0].Err)
	}
	if results[1].Err != nil || results[2].Err != nil {
		t.Errorf("the healthy SFU teardowns failed: %v, %v", results[1].Err, results[2].Err)
	}
}

func TestTeardownWebRTCConcurrently_noTasks(t *testing.T) {
	r := newDisableRig(t)
	if got := r.cm.teardownWebRTCConcurrently(context.Background(), "acme", "cluster-acme", nil); len(got) != 0 {
		t.Errorf("got %d results for no tasks", len(got))
	}
}

func TestTeardownWebRTCConcurrently_nodeWithoutAnAddressIsReported(t *testing.T) {
	r := newDisableRig(t)
	var spawned atomic.Int32
	inner := r.cm.spawnRequestFn
	r.cm.spawnRequestFn = func(ctx context.Context, ip string, req map[string]interface{}) (*spawnResponse, error) {
		spawned.Add(1)
		return inner(ctx, ip, req)
	}

	results := r.cm.teardownWebRTCConcurrently(context.Background(), "acme", "cluster-acme",
		[]webrtcTeardownTask{{NodeID: "node-9", ServiceType: "sfu", Action: teardownSFUAction}})

	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "no overlay address") {
		t.Errorf("err = %v, want the missing address reported", results[0].Err)
	}
	if spawned.Load() != 0 {
		t.Errorf("a request was sent to a node with no address")
	}
}
