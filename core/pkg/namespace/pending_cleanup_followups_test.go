package namespace

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// The local replay was not checked against the row's cluster: a stale replay on
// the node that owes it tore down a namespace created again under a new cluster.
func TestReplayRow_aLocalReplayRefusesANamespaceOfAnotherCluster(t *testing.T) {
	r := newRegistryRig(t)
	var tornDown bool
	r.cm.systemdSpawner = spawnerWithState(t, "c-new", &tornDown)
	r.pending("acme", r.cm.localNodeID, teardownAction, "c-old", true)

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if tornDown {
		t.Fatal("the local replay tore down the namespace of another cluster")
	}
	if r.pendingCount() != 1 {
		t.Fatal("a refused teardown must stay owed")
	}
}

func TestReplayRow_aLocalReplayOfItsOwnClusterTearsDown(t *testing.T) {
	r := newRegistryRig(t)
	var tornDown bool
	r.cm.systemdSpawner = spawnerWithState(t, "c-old", &tornDown)
	r.pending("acme", r.cm.localNodeID, teardownAction, "c-old", true)

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if !tornDown || r.pendingCount() != 0 {
		t.Fatalf("torn down = %v, pending = %d: the replay of its own cluster must complete", tornDown, r.pendingCount())
	}
}

// A's lease lapses mid-replay and B claims the row; A's release erased B's claim.
func TestReleasePendingClaim_doesNotEraseAnotherClaimersClaim(t *testing.T) {
	r := newRegistryRig(t)
	r.pending("acme", "node1", teardownAction, "c-old", false)
	row := r.rows()[0]
	ctx := context.Background()

	tokenA, err := r.cm.claimPendingCleanup(ctx, row)
	if err != nil || tokenA == "" {
		t.Fatalf("A's claim: %q, %v", tokenA, err)
	}
	r.exec(`UPDATE namespace_pending_cleanup SET claimed_until = datetime('now', '-1 minutes')`)
	tokenB, err := r.cm.claimPendingCleanup(ctx, row)
	if err != nil || tokenB == "" || tokenB == tokenA {
		t.Fatalf("B's claim: %q (A %q), %v", tokenB, tokenA, err)
	}

	r.cm.releasePendingClaim(ctx, row, tokenA)
	if r.count(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE claimed_by = ? AND claimed_until > datetime('now')`, tokenB) != 1 {
		t.Fatal("A's release erased B's claim")
	}
	r.cm.releasePendingClaim(ctx, row, tokenB)
	if r.count(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE claimed_by IS NULL AND claimed_until IS NULL`) != 1 {
		t.Fatal("B's own release did not clear its claim")
	}
}

// onFirstExec runs hook before the first write the wrapped client is given.
type onFirstExec struct {
	rqlite.Client
	hook func()
}

func (c *onFirstExec) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if c.hook != nil {
		h := c.hook
		c.hook = nil
		h()
	}
	return c.Client.Exec(ctx, query, args...)
}

// A row recorded again for another cluster between the read and the delete has
// not had its ports released, so the withdraw must leave it owed.
func TestWithdrawPendingTeardowns_keepsARowRecordedAgainForAnotherCluster(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c-new", "acme")
	r.allocation("c-old", "node1")
	r.pending("acme", "node1", teardownAction, "c-old", false)
	db := &onFirstExec{Client: r.client, hook: func() {
		if err := r.cm.recordPendingCleanup(context.Background(), "acme", "node1", "10.0.0.1", teardownAction,
			cleanupScope{ClusterID: "c-mid"}, errors.New("node unreachable")); err != nil {
			t.Error(err)
		}
	}}

	if err := withdrawPendingTeardowns(context.Background(), db, "c-new", "node1", teardownAction); err != nil {
		t.Fatal(err)
	}
	if r.count(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE cluster_id = 'c-mid'`) != 1 {
		t.Fatal("a row re-recorded for another cluster was deleted without its ports released")
	}
}

// An older release recorded "stop-all", which the spawn handler never had a case
// for: the row failed 30 times and would be retried hourly for ever.
func TestReplayPendingCleanups_anUnknownActionIsDroppedUnsent(t *testing.T) {
	r := newRegistryRig(t)
	r.cluster("c1", "acme")
	r.allocation("c1", "node1")
	r.pending("acme", "node1", "stop-all", "", false)
	r.pending("acme", "node1", "stop-gateway", "", false)

	if err := r.replay(); err != nil {
		t.Fatal(err)
	}
	if got := r.sent(); len(got) != 1 || got[0] != "stop-gateway:acme@node1" {
		t.Fatalf("sent %v, want only the known stop-gateway", got)
	}
	if r.count(`SELECT COUNT(*) FROM namespace_pending_cleanup WHERE action = 'stop-all'`) != 0 {
		t.Fatal("the unknown-action row was kept")
	}
	if r.count(`SELECT COUNT(*) FROM namespace_port_allocations`) != 1 {
		t.Fatal("dropping an unknown action touched the allocations")
	}
}

// The replayable set and the spawn handler's switch must not drift apart.
func TestReplayableCleanupActions_haveACaseInTheSpawnHandler(t *testing.T) {
	src, err := os.ReadFile("../gateway/handlers/namespace/spawn_handler.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range ReplayableCleanupActions {
		if !strings.Contains(string(src), `case "`+a+`"`) {
			t.Errorf("the spawn handler has no case for the replayable action %q", a)
		}
	}
	if isReplayableCleanup("stop-all") {
		t.Error("stop-all is not an action the handler implements")
	}
}
