package gateway

import (
	"context"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// Pin sweep bounds.
const (
	// pinSweepInterval is how often the cluster checks for under-replicated
	// content. Re-allocation is expensive, and content that has dropped below
	// RF is not urgent in the way a down service is — it is one more failure
	// from loss, not lost.
	pinSweepInterval = 15 * time.Minute

	// pinSweepLock is the cluster-wide lock the sweep holds, so one node does
	// the work rather than every node re-pinning the same CIDs at once.
	pinSweepLock = "ipfs-pin-sweep"

	// pinSweepLockTTL bounds a sweep. Long enough for a large inventory over a
	// slow cluster, short enough that a node that died mid-sweep does not
	// block the next one for an hour.
	pinSweepLockTTL = 30 * time.Minute
)

// StartPinSweep re-allocates under-replicated IPFS content, on one node at a
// time, until ctx is done.
//
// Pin fixes replication-min and replication-max at pin time and nothing
// revisits the allocation, so a discarded node left every CID it held below RF
// permanently: the cluster does not re-allocate on its own, and a node joining
// later received nothing. Function WASM already had a re-pin at gateway start;
// general storage content had no equivalent.
//
// The cluster lock is what makes this safe to run everywhere: every gateway
// starts the loop, exactly one does the work per interval.
func (g *Gateway) StartPinSweep(ctx context.Context, client *ipfs.Client, replicationFactor int) {
	if client == nil || replicationFactor <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(pinSweepInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.runPinSweep(ctx, client, replicationFactor)
			}
		}
	}()
}

// runPinSweep performs one sweep, if this node can take the lock.
func (g *Gateway) runPinSweep(ctx context.Context, client *ipfs.Client, replicationFactor int) {
	db := g.sqlDB
	if db == nil {
		return
	}

	holder, err := os.Hostname()
	if err != nil || holder == "" {
		holder = "unknown-host"
	}

	// Zero wait: another node holding the lock IS the sweep happening, so
	// queueing behind it would only run the same work twice in a row.
	lock, err := rqlite.AcquireClusterLock(ctx, db, pinSweepLock, holder, pinSweepLockTTL, 0)
	if err != nil {
		return
	}
	defer func() {
		if err := lock.Release(ctx); err != nil {
			g.logger.ComponentWarn(logging.ComponentGeneral,
				"Could not release the pin-sweep lock; it will expire on its own", zap.Error(err))
		}
	}()

	sweepCtx, cancel := context.WithTimeout(ctx, pinSweepLockTTL)
	defer cancel()

	result, err := client.RepinUnderReplicated(sweepCtx, replicationFactor)
	if err != nil {
		g.logger.ComponentWarn(logging.ComponentGeneral, "IPFS pin sweep failed", zap.Error(err))
		return
	}

	if result.UnderReplicated == 0 && result.Failed == 0 {
		g.logger.ComponentDebug(logging.ComponentGeneral, "IPFS pin sweep: everything is at its replication factor",
			zap.Int("examined", result.Examined))
		return
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "IPFS pin sweep re-allocated under-replicated content",
		zap.Int("examined", result.Examined),
		zap.Int("under_replicated", result.UnderReplicated),
		zap.Int("repinned", result.Repinned),
		zap.Int("failed", result.Failed),
		zap.Int("replication_factor", replicationFactor))
}
