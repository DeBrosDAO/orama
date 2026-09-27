package hub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// Aggregator assembles the cluster snapshot from this node's report and its
// peers'. Concurrent callers share one assembly, and the result is reused for
// CacheTTL, so a busy status page costs one fan-out per TTL whatever its
// audience.
type Aggregator struct {
	SelfID string
	Self   func() (*report.NodeReport, error)
	Peers  PeerLister
	Fetch  PeerFetcher

	CacheTTL time.Duration
	// PeerTimeout bounds one peer's answer.
	PeerTimeout time.Duration
	// MaxParallel bounds concurrent peer requests.
	MaxParallel int
	// StaleAfter is how old a report may be before it no longer counts: a
	// node whose collection loop stopped must not read as healthy forever.
	StaleAfter time.Duration
	// MaxUnknown is how long a node may serve no telemetry before it counts
	// as unreachable. A rolling upgrade passes well within it; a node left on
	// an old release must not drop out of the status page for good.
	MaxUnknown time.Duration
	Now        func() time.Time

	group singleflight.Group
	mu    sync.Mutex
	cache *cluster.ClusterSnapshot
	// unknownSince is when each node was first seen serving no telemetry.
	unknownSince map[string]time.Time
	// cacheAt is when the cached snapshot was finished. Freshness counts from
	// then, not from CollectedAt: an assembly slowed by a peer timing out
	// would otherwise be nearly expired before anyone read it.
	cacheAt time.Time
}

// assembleTimeout bounds one whole assembly, independent of any caller's
// context: a caller that goes away must not cancel the assembly other callers
// are waiting on.
const assembleTimeout = 15 * time.Second

// Snapshot returns a snapshot no older than CacheTTL.
func (a *Aggregator) Snapshot(ctx context.Context) (*cluster.ClusterSnapshot, error) {
	if s := a.cached(); s != nil {
		return s, nil
	}
	ch := a.group.DoChan("snapshot", func() (any, error) {
		actx, cancel := context.WithTimeout(context.Background(), assembleTimeout)
		defer cancel()
		s, err := a.assemble(actx)
		if err != nil {
			return nil, err
		}
		a.mu.Lock()
		a.cache, a.cacheAt = s, a.now()
		a.mu.Unlock()
		return s, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(*cluster.ClusterSnapshot), nil
	}
}

func (a *Aggregator) cached() *cluster.ClusterSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cache != nil && a.now().Sub(a.cacheAt) < a.CacheTTL {
		return a.cache
	}
	return nil
}

func (a *Aggregator) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Aggregator) assemble(ctx context.Context) (*cluster.ClusterSnapshot, error) {
	start := a.now()
	peers, err := a.Peers.Peers(ctx)
	if err != nil {
		return nil, fmt.Errorf("assemble cluster snapshot: %w", err)
	}
	snap := &cluster.ClusterSnapshot{
		CollectedAt: start.UTC(),
		Nodes:       make([]cluster.CollectionStatus, len(peers)),
	}
	sem := make(chan struct{}, max(a.MaxParallel, 1))
	var wg sync.WaitGroup
	for i, p := range peers {
		wg.Add(1)
		go func(i int, p Peer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			snap.Nodes[i] = a.collectPeer(ctx, p)
		}(i, p)
	}
	wg.Wait()
	a.forgetUnknownExcept(peers)
	snap.DurationMS = a.now().Sub(start).Milliseconds()
	snap.Alerts = cluster.DeriveAlerts(snap)
	return snap, nil
}

// collectPeer gets one node's report, from memory for this node and over the
// mesh for the others.
func (a *Aggregator) collectPeer(ctx context.Context, p Peer) cluster.CollectionStatus {
	start := a.now()
	cs := cluster.CollectionStatus{Node: cluster.NodeRef{Host: p.PublicIP, Role: p.Role, WGIP: p.WGIP}}
	pr, err := a.report(ctx, p)
	if !errors.Is(err, ErrPeerWithoutTelemetry) {
		a.clearUnknown(p.ID)
	}
	cs.DurationMS = a.now().Sub(start).Milliseconds()
	cs.ReportAgeSec = int(pr.Age.Seconds())
	switch {
	case errors.Is(err, ErrPeerWithoutTelemetry) && a.unknownFor(p.ID) > a.MaxUnknown:
		cs.Err = fmt.Sprintf("has served no telemetry for over %s: it still runs an older release; upgrade it", a.MaxUnknown)
	case errors.Is(err, ErrPeerWithoutTelemetry):
		cs.Unknown, cs.Err = true, err.Error()
	case pr.Report == nil && err != nil:
		cs.Err = err.Error()
	case pr.Report == nil:
		cs.Err = "no report"
	case pr.Age > a.StaleAfter:
		cs.Err = fmt.Sprintf("newest report is %ds old: this node's health collection has stopped", cs.ReportAgeSec)
	default:
		// The report does not know the address operators know the node by.
		// It is copied first: this node's report is shared with every reader.
		rc := *pr.Report
		rc.PublicIP = p.PublicIP
		cs.Report = &rc
	}
	return cs
}

// report is this node's report from memory, or a peer's over the mesh.
func (a *Aggregator) report(ctx context.Context, p Peer) (PeerReport, error) {
	if p.ID != a.SelfID {
		pctx, cancel := context.WithTimeout(ctx, a.PeerTimeout)
		defer cancel()
		return a.Fetch.Fetch(pctx, p)
	}
	r, err := a.Self()
	if r == nil {
		return PeerReport{}, err
	}
	return PeerReport{Report: r, Age: ReportAge(r, a.now())}, err
}

// ReportAge is how old r is by this node's clock, never negative.
func ReportAge(r *report.NodeReport, now time.Time) time.Duration {
	return max(now.Sub(r.Timestamp), 0)
}

// unknownFor is how long p has served no telemetry, starting the clock on
// the first sighting.
func (a *Aggregator) unknownFor(id string) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.unknownSince == nil {
		a.unknownSince = map[string]time.Time{}
	}
	since, ok := a.unknownSince[id]
	if !ok {
		since = a.now()
		a.unknownSince[id] = since
	}
	return a.now().Sub(since)
}

func (a *Aggregator) clearUnknown(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.unknownSince, id)
}

// forgetUnknownExcept drops the unknown-since clocks of nodes that have left
// the registry.
func (a *Aggregator) forgetUnknownExcept(peers []Peer) {
	current := make(map[string]bool, len(peers))
	for _, p := range peers {
		current[p.ID] = true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id := range a.unknownSince {
		if !current[id] {
			delete(a.unknownSince, id)
		}
	}
}
