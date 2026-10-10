package setup

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestParallel_aFailureStopsTheMachinesStillQueued(t *testing.T) {
	r := &runner{clusterSize: 3} // one machine at a time
	nodes := []*nodeRun{{}, {}, {}, {}}
	var started atomic.Int32
	boom := errors.New("boom")
	err := r.parallel(context.Background(), nodes, func(context.Context, *nodeRun) error {
		started.Add(1)
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the first failure", err)
	}
	if got := started.Load(); got != 1 {
		t.Errorf("%d machines were started, the ones queued behind the failure must not be", got)
	}
}

func TestParallel_theContextOfARunningMachineIsCancelledByAnotherFailure(t *testing.T) {
	r := &runner{clusterSize: 5}
	running := make(chan struct{})
	var cancelled atomic.Bool
	var calls atomic.Int32
	err := r.parallel(context.Background(), []*nodeRun{{}, {}}, func(ctx context.Context, _ *nodeRun) error {
		if calls.Add(1) == 1 {
			<-running
			return errors.New("first fails")
		}
		close(running)
		<-ctx.Done()
		cancelled.Store(true)
		return ctx.Err()
	})
	if err == nil || err.Error() != "first fails" {
		t.Fatalf("err = %v", err)
	}
	if !cancelled.Load() {
		t.Error("the machine still running was not told to stop")
	}
}

func TestParallel_noNodesIsNoError(t *testing.T) {
	r := &runner{clusterSize: 1}
	if err := r.parallel(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
}
