package namespace

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/rqlite/rqlitetest"
	"go.uber.org/zap"
)

// webrtcRegistry is the real registry schema in SQLite with a WebRTC port
// allocator over it, so the unique indexes of migration 067 are the ones the
// allocator meets.
func webrtcRegistry(t *testing.T) (*WebRTCPortAllocator, rqlite.Client, *sql.DB) {
	t.Helper()
	c, db := rqlitetest.SQLite(t)
	if err := rqlite.ApplyEmbeddedMigrations(context.Background(), db, migrations.FS, zap.NewNop()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return NewWebRTCPortAllocator(c, zap.NewNop()), c, db
}

func sfuBlock(id, node, cluster string, signaling, mediaStart int) *WebRTCPortBlock {
	return &WebRTCPortBlock{
		ID: id, NodeID: node, NamespaceClusterID: cluster, ServiceType: "sfu",
		SFUSignalingPort: signaling, SFUMediaPortStart: mediaStart, SFUMediaPortEnd: mediaStart + SFUMediaPortsPerNamespace - 1,
	}
}

func turnBlock(id, node, cluster string, relayStart int) *WebRTCPortBlock {
	return &WebRTCPortBlock{
		ID: id, NodeID: node, NamespaceClusterID: cluster, ServiceType: "turn",
		TURNListenPort: TURNDefaultPort, TURNTLSPort: TURNSPort,
		TURNRelayPortStart: relayStart, TURNRelayPortEnd: relayStart + TURNRelayPortsPerNamespace - 1,
	}
}

// The bug: two namespaces on one node were given SFU signaling port 30000 and
// media 20000-20499 because nothing but the allocator's own read kept them
// apart. The registry now refuses the second row.
func TestInsertPortBlock_refusesAPortAnotherClusterHoldsOnTheNode(t *testing.T) {
	wpa, _, _ := webrtcRegistry(t)
	ctx := context.Background()
	if err := wpa.insertPortBlock(ctx, sfuBlock("a", "node-1", "c-a", 30000, 20000)); err != nil {
		t.Fatalf("first allocation: %v", err)
	}

	for name, dup := range map[string]*WebRTCPortBlock{
		"same signaling port":    sfuBlock("b", "node-1", "c-b", 30000, 20500),
		"same media range start": sfuBlock("c", "node-1", "c-c", 30001, 20000),
	} {
		t.Run(name, func(t *testing.T) {
			err := wpa.insertPortBlock(ctx, dup)
			if err == nil {
				t.Fatal("a second cluster was given ports the first one holds")
			}
			if !isConflictError(err) {
				t.Errorf("err = %v; the allocator's retry has to recognise it as a conflict", err)
			}
		})
	}
}

func TestInsertPortBlock_theSamePortsOnAnotherNodeAreFine(t *testing.T) {
	wpa, _, _ := webrtcRegistry(t)
	ctx := context.Background()
	if err := wpa.insertPortBlock(ctx, sfuBlock("a", "node-1", "c-a", 30000, 20000)); err != nil {
		t.Fatal(err)
	}
	if err := wpa.insertPortBlock(ctx, sfuBlock("b", "node-2", "c-b", 30000, 20000)); err != nil {
		t.Fatalf("the same ports on a different node are not a collision: %v", err)
	}
}

func TestInsertPortBlock_refusesATURNRelayRangeTwiceOnANode(t *testing.T) {
	wpa, _, _ := webrtcRegistry(t)
	ctx := context.Background()
	if err := wpa.insertPortBlock(ctx, turnBlock("a", "node-1", "c-a", TURNRelayPortRangeStart)); err != nil {
		t.Fatal(err)
	}
	if err := wpa.insertPortBlock(ctx, turnBlock("b", "node-1", "c-b", TURNRelayPortRangeStart)); err == nil {
		t.Fatal("two TURN allocations got the same relay range")
	}
	if err := wpa.insertPortBlock(ctx, turnBlock("c", "node-1", "c-c", TURNRelayPortRangeStart+TURNRelayPortsPerNamespace)); err != nil {
		t.Fatalf("the next relay range is free: %v", err)
	}
}

// An SFU row stores zero for the TURN columns and a TURN row zero for the SFU
// ones; the zeros of different rows must not collide.
func TestInsertPortBlock_zeroColumnsOfOtherServicesDoNotCollide(t *testing.T) {
	wpa, _, _ := webrtcRegistry(t)
	ctx := context.Background()
	for i, c := range []string{"c-a", "c-b", "c-c"} {
		if err := wpa.insertPortBlock(ctx, sfuBlock("s"+c, "node-1", c, 30000+i, SFUMediaPortRangeStart+i*SFUMediaPortsPerNamespace)); err != nil {
			t.Fatalf("sfu %s: %v", c, err)
		}
		if err := wpa.insertPortBlock(ctx, turnBlock("t"+c, "node-1", c, TURNRelayPortRangeStart+i*TURNRelayPortsPerNamespace)); err != nil {
			t.Fatalf("turn %s: %v", c, err)
		}
	}
}

// staleReads hides the allocations from the first n port queries, as the
// allocator sees them when its read missed a row another allocation just wrote.
type staleReads struct {
	rqlite.Client
	remaining atomic.Int32
}

func (s *staleReads) Query(ctx context.Context, dest any, query string, args ...any) error {
	if strings.Contains(query, "port_val") && s.remaining.Add(-1) >= 0 {
		return nil
	}
	return s.Client.Query(ctx, dest, query, args...)
}

// The allocator that lost the race picks the next free ports instead of
// failing or double-booking: the conflict is retried against a fresh read.
func TestAllocateSFUPorts_aLostRaceTakesTheNextFreePorts(t *testing.T) {
	_, c, _ := webrtcRegistry(t)
	ctx := context.Background()
	first := NewWebRTCPortAllocator(c, zap.NewNop())
	if err := first.insertPortBlock(ctx, sfuBlock("a", "node-1", "c-a", SFUSignalingPortRangeStart, SFUMediaPortRangeStart)); err != nil {
		t.Fatal(err)
	}

	stale := &staleReads{Client: c}
	stale.remaining.Store(2) // the signaling and media reads of the first attempt
	racer := NewWebRTCPortAllocator(stale, zap.NewNop())

	got, err := racer.AllocateSFUPorts(ctx, "node-1", "c-b")
	if err != nil {
		t.Fatalf("AllocateSFUPorts: %v", err)
	}
	if got.SFUSignalingPort != SFUSignalingPortRangeStart+1 || got.SFUMediaPortStart != SFUMediaPortRangeStart+SFUMediaPortsPerNamespace {
		t.Errorf("got signaling %d media %d, want the next free %d / %d",
			got.SFUSignalingPort, got.SFUMediaPortStart, SFUSignalingPortRangeStart+1, SFUMediaPortRangeStart+SFUMediaPortsPerNamespace)
	}
}

func TestAllocateSFUPorts_neverHandsOutAHeldBlock(t *testing.T) {
	wpa, _, _ := webrtcRegistry(t)
	ctx := context.Background()
	seen := map[int]bool{}
	for i := 0; i < SFUMediaPortRangeEnd/SFUMediaPortsPerNamespace-SFUMediaPortRangeStart/SFUMediaPortsPerNamespace+1; i++ {
		b, err := wpa.AllocateSFUPorts(ctx, "node-1", "cluster-"+string(rune('a'+i)))
		if err != nil {
			t.Fatalf("allocation %d: %v", i, err)
		}
		if seen[b.SFUMediaPortStart] {
			t.Fatalf("media start %d was handed out twice", b.SFUMediaPortStart)
		}
		seen[b.SFUMediaPortStart] = true
	}
	if _, err := wpa.AllocateSFUPorts(ctx, "node-1", "one-too-many"); err == nil {
		t.Fatal("an allocation succeeded with every media block held")
	}
}

func TestRecordSFUPorts(t *testing.T) {
	ctx := context.Background()

	t.Run("records the ports a running unit holds, and nobody else is given them", func(t *testing.T) {
		wpa, _, _ := webrtcRegistry(t)
		if _, err := wpa.RecordSFUPorts(ctx, "node-1", "c-old", 30000, 20000, 20499); err != nil {
			t.Fatal(err)
		}
		next, err := wpa.AllocateSFUPorts(ctx, "node-1", "c-new")
		if err != nil {
			t.Fatal(err)
		}
		if next.SFUSignalingPort == 30000 || next.SFUMediaPortStart == 20000 {
			t.Fatalf("the new namespace was given the held ports: %+v", next)
		}
	})

	t.Run("is idempotent for the same ports", func(t *testing.T) {
		wpa, _, db := webrtcRegistry(t)
		for i := 0; i < 2; i++ {
			if _, err := wpa.RecordSFUPorts(ctx, "node-1", "c-old", 30000, 20000, 20499); err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM webrtc_port_allocations`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("rows = %d (%v), want 1", n, err)
		}
	})

	t.Run("a row naming other ports is an error, not an overwrite", func(t *testing.T) {
		wpa, _, _ := webrtcRegistry(t)
		if err := wpa.insertPortBlock(ctx, sfuBlock("a", "node-1", "c-old", 30001, 20500)); err != nil {
			t.Fatal(err)
		}
		_, err := wpa.RecordSFUPorts(ctx, "node-1", "c-old", 30000, 20000, 20499)
		if err == nil || !strings.Contains(err.Error(), "30001") {
			t.Fatalf("err = %v, want the disagreement reported", err)
		}
	})

	t.Run("ports another cluster already holds are a conflict", func(t *testing.T) {
		wpa, _, _ := webrtcRegistry(t)
		if err := wpa.insertPortBlock(ctx, sfuBlock("a", "node-1", "c-new", 30000, 20000)); err != nil {
			t.Fatal(err)
		}
		_, err := wpa.RecordSFUPorts(ctx, "node-1", "c-old", 30000, 20000, 20499)
		if err == nil || !isConflictError(err) {
			t.Fatalf("err = %v, want the unique index's conflict", err)
		}
	})
}
