package namespace

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

var errNotLeader = errors.New("failed to query active nodes: not leader")

func TestRetryWhileNoLeader_retries_then_succeeds(t *testing.T) {
	var calls int32
	err := retryWhileNoLeader(context.Background(), zap.NewNop(), "op", func(context.Context) error {
		if atomic.AddInt32(&calls, 1) < 3 {
			return errNotLeader
		}
		return nil
	})
	if err != nil {
		t.Fatalf("want success after the election, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetryWhileNoLeader_non_transient_error_returns_at_once(t *testing.T) {
	want := errors.New("UNIQUE constraint failed: namespace_clusters.id")
	var calls int32
	err := retryWhileNoLeader(context.Background(), zap.NewNop(), "op", func(context.Context) error {
		atomic.AddInt32(&calls, 1)
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("err = %v, calls = %d; want the original error after 1 call", err, calls)
	}
}

func TestRetryWhileNoLeader_stops_at_ctx_deadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := retryWhileNoLeader(ctx, zap.NewNop(), "select nodes", func(context.Context) error { return errNotLeader })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	if !strings.Contains(err.Error(), "not leader") || !strings.Contains(err.Error(), "select nodes") {
		t.Fatalf("error must say what was waiting and why: %v", err)
	}
}

func TestMarkProvisioningFailed_writes_through_a_transient_failure(t *testing.T) {
	var execs int32
	db := &recoveryMockDB{execFunc: func(string, ...any) error {
		if atomic.AddInt32(&execs, 1) == 1 {
			return errNotLeader
		}
		return nil
	}}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}

	cm.markProvisioningFailed("cl-1", "ns", "boom")

	if execs != 2 {
		t.Fatalf("execs = %d, want the failed write retried once", execs)
	}
	last := db.execCalls[len(db.execCalls)-1]
	if last.Args[0] != ClusterStatusFailed || last.Args[1] != "boom" || last.Args[2] != "cl-1" {
		t.Fatalf("wrote %v, want failed/boom/cl-1", last.Args)
	}
}

func TestMarkProvisioningFailed_logs_a_permanent_failure(t *testing.T) {
	db := &recoveryMockDB{execFunc: func(string, ...any) error { return errors.New("disk I/O error") }}
	core, logs := observer.New(zap.ErrorLevel)
	cm := &ClusterManager{db: db, logger: zap.New(core)}

	cm.markProvisioningFailed("cl-1", "ns", "boom")

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("want one error log, got %d", len(entries))
	}
	f := entries[0].ContextMap()
	if f["namespace"] != "ns" || f["cluster_id"] != "cl-1" || f["failure"] != "boom" {
		t.Fatalf("log must carry namespace, cluster_id and the reason: %v", f)
	}
}

func TestSelectNodesWaitingForLeader_proceeds_once_a_leader_exists(t *testing.T) {
	var queries int32
	db := &recoveryMockDB{queryFunc: func(any, string, ...any) error {
		if atomic.AddInt32(&queries, 1) == 1 {
			return errNotLeader
		}
		return nil
	}}
	cm := &ClusterManager{db: db, logger: zap.NewNop()}
	cm.portAllocator = NewNamespacePortAllocator(db, zap.NewNop())
	cm.nodeSelector = NewClusterNodeSelector(db, cm.portAllocator, zap.NewNop())

	_, err := cm.selectNodesWaitingForLeader(context.Background(), 3)

	// Past the election the registry answers with an empty fleet: a real,
	// non-transient verdict, reached only because the first read was retried.
	var ce *ClusterError
	if !errors.As(err, &ce) || ce.Message != ErrInsufficientNodes.Message {
		t.Fatalf("want insufficient nodes after the retry, got %v", err)
	}
	if queries < 2 {
		t.Fatalf("queries = %d, want the not-leader read retried", queries)
	}
}
