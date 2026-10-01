package namespace

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

var errUniqueClusterNode = errors.New("UNIQUE constraint failed: namespace_port_allocations.namespace_cluster_id, namespace_port_allocations.node_id")

// allocatorMock serves GetPortBlock from `existing` (nil = none) and fails
// every INSERT with insertErr.
func allocatorMock(existing func() *PortBlock, getErr error, insertErr func() error) (*NamespacePortAllocator, *recoveryMockDB) {
	db := &recoveryMockDB{
		queryFunc: func(dest any, query string, _ ...any) error {
			if d, ok := dest.(*[]PortBlock); ok {
				if getErr != nil {
					return getErr
				}
				if b := existing(); b != nil {
					*d = []PortBlock{*b}
				}
			}
			return nil
		},
		execFunc: func(query string, _ ...any) error {
			if strings.Contains(query, "INSERT INTO namespace_port_allocations") {
				return insertErr()
			}
			return nil
		},
	}
	return NewNamespacePortAllocator(db, zap.NewNop()), db
}

func TestAllocatePortBlock_committed_insert_with_lost_reply_returns_the_existing_block(t *testing.T) {
	var committed atomic.Bool
	want := &PortBlock{ID: "b1", NodeID: "n1", NamespaceClusterID: "c1", PortStart: 10000, PortEnd: 10004}
	npa, _ := allocatorMock(
		func() *PortBlock {
			if committed.Load() {
				return want
			}
			return nil
		},
		nil,
		func() error { committed.Store(true); return errUniqueClusterNode },
	)

	got, err := npa.AllocatePortBlock(context.Background(), "n1", "c1", BlueprintTenant())
	if err != nil {
		t.Fatalf("a retry whose first insert committed must succeed, got %v", err)
	}
	if got.ID != want.ID || got.PortStart != want.PortStart {
		t.Fatalf("got block %+v, want the committed one %+v", got, want)
	}
}

func TestAllocatePortBlock_existing_block_is_returned_without_insert(t *testing.T) {
	want := &PortBlock{ID: "b1", PortStart: 10005}
	npa, db := allocatorMock(func() *PortBlock { return want }, nil, func() error { return nil })

	got, err := npa.AllocatePortBlock(context.Background(), "n1", "c1", BlueprintTenant())
	if err != nil || got.ID != "b1" {
		t.Fatalf("got %+v, %v; want the existing block", got, err)
	}
	if countExecs(db, "INSERT INTO namespace_port_allocations") != 0 {
		t.Fatal("an existing allocation must not be inserted again")
	}
}

func TestAllocatePortBlock_lookup_error_is_reported_not_ignored(t *testing.T) {
	boom := errors.New("registry unreachable")
	npa, db := allocatorMock(func() *PortBlock { return nil }, boom, func() error { return nil })

	_, err := npa.AllocatePortBlock(context.Background(), "n1", "c1", BlueprintTenant())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the lookup failure", err)
	}
	if countExecs(db, "INSERT INTO namespace_port_allocations") != 0 {
		t.Fatal("must not allocate when it cannot tell whether a block already exists")
	}
}

func TestAllocatePortBlock_lost_race_for_a_block_retries_and_succeeds(t *testing.T) {
	var inserts int32
	npa, _ := allocatorMock(
		func() *PortBlock { return nil }, nil,
		func() error {
			if atomic.AddInt32(&inserts, 1) == 1 {
				return errors.New("UNIQUE constraint failed: namespace_port_allocations.node_id, namespace_port_allocations.port_start")
			}
			return nil
		})

	got, err := npa.AllocatePortBlock(context.Background(), "n1", "c1", BlueprintTenant())
	if err != nil || got == nil {
		t.Fatalf("got %v, %v; want a block after one lost race", got, err)
	}
	if inserts != 2 {
		t.Fatalf("inserts = %d, want 2", inserts)
	}
}

func TestAllocatePortBlock_backoff_stops_at_ctx_end(t *testing.T) {
	npa, _ := allocatorMock(func() *PortBlock { return nil }, nil,
		func() error { return errors.New("UNIQUE constraint failed: port_start") })
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := npa.AllocatePortBlock(ctx, "n1", "c1", BlueprintTenant())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("backoff ignored ctx: took %v", elapsed)
	}
}

// sharedIPMock answers the node-IP lookup with ip, the shared-IP count with
// count, and fails the query whose text contains failOn (if non-empty).
func sharedIPMock(ip string, count int, failOn string, boom error) *NamespacePortAllocator {
	db := &recoveryMockDB{
		queryFunc: func(dest any, query string, _ ...any) error {
			if failOn != "" && strings.Contains(query, failOn) {
				return boom
			}
			switch d := dest.(type) {
			case *[]struct {
				IPAddress string `db:"ip_address"`
			}:
				*d = []struct {
					IPAddress string `db:"ip_address"`
				}{{IPAddress: ip}}
			case *[]struct {
				Count int `db:"count"`
			}:
				*d = []struct {
					Count int `db:"count"`
				}{{Count: count}}
			}
			return nil
		},
	}
	return NewNamespacePortAllocator(db, zap.NewNop())
}

func TestAllocatedRanges_shared_ip_count_error_is_reported(t *testing.T) {
	boom := errors.New("count unreachable")
	npa := sharedIPMock("1.2.3.4", 2, "COUNT(DISTINCT id)", boom)

	_, err := npa.allocatedRanges(context.Background(), "n1")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the count failure", err)
	}
}

func TestAllocatedRanges_shared_ip_range_query_error_is_reported(t *testing.T) {
	boom := errors.New("ranges unreachable")
	npa := sharedIPMock("1.2.3.4", 2, "JOIN dns_nodes", boom)

	_, err := npa.allocatedRanges(context.Background(), "n1")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the range query failure", err)
	}
}

func TestAllocatedRanges_shared_ip_with_no_allocations_is_empty(t *testing.T) {
	npa := sharedIPMock("1.2.3.4", 2, "", nil)

	got, err := npa.allocatedRanges(context.Background(), "n1")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want no ranges and no error", got, err)
	}
}
