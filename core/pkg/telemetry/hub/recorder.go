package hub

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// RecordInterval is one uptime sample: the store counts minutes.
const RecordInterval = time.Minute

// activeStatus is dns_nodes.status for a node that is heartbeating.
const activeStatus = "active"

// UptimeRecorder samples the cluster's component states once a minute and
// records them. Every cluster gateway runs it, and exactly one writes: the
// active node with the lowest id. When that node stops heartbeating the
// registry marks it inactive and the next one takes over, so no election is
// needed and no raft write depends on this node being the raft leader.
type UptimeRecorder struct {
	SelfID   string
	Snapshot func(ctx context.Context) (*cluster.ClusterSnapshot, error)
	Peers    PeerLister
	Store    UptimeStore
	Logger   *zap.Logger
	Now      func() time.Time
}

// Run records every RecordInterval until ctx is done.
func (u *UptimeRecorder) Run(ctx context.Context) {
	ticker := time.NewTicker(RecordInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := u.recordSafely(ctx); err != nil {
				u.logger().Warn("service uptime not recorded this minute", zap.Error(err))
			}
		}
	}
}

// recordSafely is one sample, with a panic turned into an error: this runs
// in the cluster gateway, and a sample is not worth the process.
func (u *UptimeRecorder) recordSafely(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("uptime sample panicked: %v", r)
		}
	}()
	return u.recordOnce(ctx)
}

func (u *UptimeRecorder) logger() *zap.Logger {
	if u.Logger != nil {
		return u.Logger
	}
	return zap.NewNop()
}

func (u *UptimeRecorder) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *UptimeRecorder) recordOnce(ctx context.Context) error {
	peers, err := u.Peers.Peers(ctx)
	if err != nil {
		return err
	}
	if !IsUptimeWriter(u.SelfID, peers) {
		return nil
	}
	snap, err := u.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("take cluster snapshot for uptime: %w", err)
	}
	if hasUnknownNode(snap) {
		// Mid-way through a rolling upgrade some nodes serve no telemetry.
		// What the rest say is not the cluster's state, and the record is
		// public and kept for 90 days, so the minute is not recorded.
		return nil
	}
	now := u.now()
	if err := u.Store.Record(ctx, now, cluster.Components(snap)); err != nil {
		return err
	}
	// Once an hour is enough to keep the table to its window.
	if now.UTC().Minute() == 0 {
		return u.Store.Prune(ctx, now)
	}
	return nil
}

// IsUptimeWriter reports whether selfID is the active node with the lowest id.
// peers must be in id order, as PeerLister returns them.
func IsUptimeWriter(selfID string, peers []Peer) bool {
	for _, p := range peers {
		if p.Status != activeStatus {
			continue
		}
		return p.ID == selfID
	}
	return false
}

func hasUnknownNode(snap *cluster.ClusterSnapshot) bool {
	for _, n := range snap.Nodes {
		if n.Unknown {
			return true
		}
	}
	return false
}
