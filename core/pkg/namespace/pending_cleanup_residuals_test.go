package namespace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// onExecMatching runs hook before the first write whose statement contains match.
type onExecMatching struct {
	rqlite.Client
	match string
	hook  func()
}

func (c *onExecMatching) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if c.hook != nil && strings.Contains(query, c.match) {
		h := c.hook
		c.hook = nil
		h()
	}
	return c.Client.Exec(ctx, query, args...)
}

// A re-add withdraws the row while the replay's teardown is on the wire. When the
// teardown then fails, recording the failure inserted the row again: the
// re-added node's block was hidden from the cluster's reads and the next replay
// sent a teardown to the node being spawned.
func TestReplayRow_aFailedSendDoesNotRecreateAWithdrawnRow(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	r.allocation("c1", "node1")
	r.pending("acme", "node1", teardownAction, "c1", false)
	row := r.rows()[0]
	r.cm.spawnRequestFn = func(context.Context, string, map[string]interface{}) (*spawnResponse, error) {
		r.exec(`DELETE FROM namespace_pending_cleanup`)
		return nil, errors.New("node unreachable")
	}

	if err := r.cm.replayRow(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if n := r.pendingCount(); n != 0 {
		t.Fatalf("%d pending rows after the failed send; the withdrawn row was recreated", n)
	}
}

func TestReplayRow_aFailedSendCountsOnTheRowItHolds(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	r.allocation("c1", "node1")
	r.pending("acme", "node1", teardownAction, "c1", false)
	row := r.rows()[0]
	r.sendErr = errors.New("node unreachable again")

	if err := r.cm.replayRow(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	rows := r.rows()
	if len(rows) != 1 || rows[0].Attempts != row.Attempts+1 {
		t.Fatalf("rows %+v, want one row with attempts %d", rows, row.Attempts+1)
	}
	if r.count(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE claimed_by IS NULL AND last_error = 'node unreachable again'`) != 1 {
		t.Fatal("the failure was not recorded on the row, or its claim was kept")
	}
}

// The eviction used to remove the membership first: a crash before the owed row
// was recorded left a block nothing owed and nothing freed. The row has to exist,
// claimed so a replay leaves it be, while the membership row is deleted.
func TestRemoveAndEvictMember_recordsTheOwedTeardownBeforeRemovingTheMembership(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	r.registerNode("node1")
	r.allocation("c1", "node1")
	r.membership("c1", "node1")
	r.sendErr = errors.New("node unreachable")

	checked := false
	r.cm.db = &onExecMatching{Client: r.client, match: "DELETE FROM namespace_cluster_nodes", hook: func() {
		checked = true
		if r.count(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE node_id = 'node1' AND cluster_id = 'c1' AND claimed_by IS NOT NULL`) != 1 {
			t.Error("no claimed owed row existed when the membership was removed")
		}
		if err := r.replay(); err != nil {
			t.Error(err)
		}
		if len(r.sent()) != 0 || r.pendingCount() != 1 {
			t.Errorf("a replay acted on the row being recorded: sent %v, rows %d", r.sent(), r.pendingCount())
		}
	}}

	if !r.cm.removeAndEvictMember(context.Background(), "c1", "acme", "node1") {
		t.Fatal("the eviction was refused")
	}
	if !checked {
		t.Fatal("the membership was never removed")
	}
	if r.count(`SELECT COUNT(*) FROM namespace_cluster_nodes`) != 0 {
		t.Fatal("membership kept")
	}
	if r.count(`SELECT COUNT(*) FROM namespace_port_allocations`) != 1 {
		t.Fatal("an unconfirmed teardown freed the block")
	}
	if r.count(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE claimed_by IS NULL`) != 1 {
		t.Fatal("the teardown is not left owed and unclaimed for the replay")
	}
}

func TestRemoveAndEvictMember_aConfirmedTeardownLeavesNothingOwed(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	r.registerNode("node1")
	r.allocation("c1", "node1")
	r.membership("c1", "node1")

	r.cm.removeAndEvictMember(context.Background(), "c1", "acme", "node1")
	if r.pendingCount() != 0 || r.count(`SELECT COUNT(*) FROM namespace_port_allocations`) != 0 {
		t.Fatal("a confirmed teardown left a row or a block")
	}
}

// A node gone from the registry sends no teardown, so nothing else would clear
// the row recorded before the membership went.
func TestRemoveAndEvictMember_aRemovedNodeLeavesNothingOwed(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	r.allocation("c1", "node1")
	r.membership("c1", "node1")

	r.cm.removeAndEvictMember(context.Background(), "c1", "acme", "node1")
	if len(r.sent()) != 0 || r.pendingCount() != 0 || r.count(`SELECT COUNT(*) FROM namespace_port_allocations`) != 0 {
		t.Fatalf("sent %v, rows %d: a removed node's eviction left state behind", r.sent(), r.pendingCount())
	}
}
