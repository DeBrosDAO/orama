package hub

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

type staticPeers []Peer

func (s staticPeers) Peers(context.Context) ([]Peer, error) { return s, nil }

type failingPeers struct{}

func (failingPeers) Peers(context.Context) ([]Peer, error) { return nil, errors.New("rqlite down") }

// fakeFetcher answers from a map by peer id and counts calls.
type fakeFetcher struct {
	reports map[string]*report.NodeReport
	errs    map[string]error
	calls   atomic.Int32
	delay   time.Duration
}

func (f *fakeFetcher) Fetch(ctx context.Context, p Peer) (PeerReport, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if err := f.errs[p.ID]; err != nil {
		return PeerReport{}, err
	}
	r := f.reports[p.ID]
	if r == nil {
		return PeerReport{}, nil
	}
	// A peer measures age on its own clock; here, the report's timestamp.
	return PeerReport{Report: r, Age: testNow.Sub(r.Timestamp)}, nil
}

var testNow = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func freshReport() *report.NodeReport {
	return &report.NodeReport{Timestamp: testNow.Add(-5 * time.Second)}
}

func newAggregator(peers PeerLister, fetch PeerFetcher, self *report.NodeReport) *Aggregator {
	return &Aggregator{
		SelfID:      "self",
		Self:        func() (*report.NodeReport, error) { return self, nil },
		Peers:       peers,
		Fetch:       fetch,
		CacheTTL:    5 * time.Second,
		PeerTimeout: time.Second,
		MaxParallel: 4,
		StaleAfter:  time.Minute,
		MaxUnknown:  time.Hour,
		Now:         func() time.Time { return testNow },
	}
}

func TestAggregatorSnapshot_selfFromMemoryPeersOverMesh(t *testing.T) {
	peers := staticPeers{{ID: "self", PublicIP: "1.1.1.1", Role: "nameserver-ns1"}, {ID: "p2", PublicIP: "2.2.2.2"}}
	fetch := &fakeFetcher{reports: map[string]*report.NodeReport{"p2": freshReport()}}
	self := freshReport()
	snap, err := newAggregator(peers, fetch, self).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fetch.calls.Load() != 1 {
		t.Errorf("fetched %d peers over the mesh, want 1 (self is read from memory)", fetch.calls.Load())
	}
	if snap.HealthyCount() != 2 || snap.Nodes[0].Node.Role != "nameserver-ns1" {
		t.Fatalf("snapshot = %+v", snap.Nodes)
	}
	if snap.Nodes[0].Report.PublicIP != "1.1.1.1" {
		t.Errorf("public ip = %q, want the registry's", snap.Nodes[0].Report.PublicIP)
	}
	if self.PublicIP != "" {
		t.Error("the shared self report was modified")
	}
	if snap.Nodes[0].ReportAgeSec != 5 {
		t.Errorf("report age = %d, want 5", snap.Nodes[0].ReportAgeSec)
	}
}

func TestAggregatorSnapshot_unreachableAndStalePeers(t *testing.T) {
	stale := &report.NodeReport{Timestamp: testNow.Add(-10 * time.Minute)}
	peers := staticPeers{{ID: "down"}, {ID: "stale"}}
	fetch := &fakeFetcher{
		errs:    map[string]error{"down": errors.New("connection refused")},
		reports: map[string]*report.NodeReport{"stale": stale},
	}
	snap, err := newAggregator(peers, fetch, nil).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Nodes[0].Err != "connection refused" || snap.Nodes[0].Report != nil {
		t.Errorf("down peer = %+v", snap.Nodes[0])
	}
	if snap.Nodes[1].Report != nil || snap.Nodes[1].Err == "" {
		t.Errorf("stale report counted as fresh: %+v", snap.Nodes[1])
	}
	if len(snap.Alerts) != 2 {
		t.Errorf("alerts = %+v, want one collection alert per node", snap.Alerts)
	}
}

func TestAggregatorSnapshot_registryErrorIsReturned(t *testing.T) {
	if _, err := newAggregator(failingPeers{}, &fakeFetcher{}, nil).Snapshot(context.Background()); err == nil {
		t.Fatal("expected an error when the node list cannot be read")
	}
}

func TestAggregatorSnapshot_selfWithoutReport(t *testing.T) {
	a := newAggregator(staticPeers{{ID: "self"}}, &fakeFetcher{}, nil)
	a.Self = func() (*report.NodeReport, error) { return nil, ErrNoReportYet }
	snap, err := a.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Nodes[0].Err != ErrNoReportYet.Error() {
		t.Fatalf("self = %+v", snap.Nodes[0])
	}
}

func TestAggregatorSnapshot_cachesAndSharesOneAssembly(t *testing.T) {
	fetch := &fakeFetcher{reports: map[string]*report.NodeReport{"p": freshReport()}, delay: 50 * time.Millisecond}
	a := newAggregator(staticPeers{{ID: "p"}}, fetch, nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Snapshot(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := a.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := fetch.calls.Load(); n != 1 {
		t.Fatalf("21 callers caused %d fan-outs, want 1", n)
	}
}

func TestAggregatorSnapshot_callerCancelDoesNotBlock(t *testing.T) {
	fetch := &fakeFetcher{reports: map[string]*report.NodeReport{"p": freshReport()}, delay: 200 * time.Millisecond}
	a := newAggregator(staticPeers{{ID: "p"}}, fetch, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := a.Snapshot(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the caller's deadline", err)
	}
}

func TestAggregatorSnapshot_noPeers(t *testing.T) {
	snap, err := newAggregator(staticPeers{}, &fakeFetcher{}, nil).Snapshot(context.Background())
	if err != nil || len(snap.Nodes) != 0 {
		t.Fatalf("snap=%+v err=%v", snap, err)
	}
}

func TestAggregatorSnapshot_oldReleasePeerIsUnknown(t *testing.T) {
	fetch := &fakeFetcher{errs: map[string]error{"old": ErrPeerWithoutTelemetry}}
	snap, err := newAggregator(staticPeers{{ID: "old", PublicIP: "3.3.3.3"}}, fetch, nil).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n := snap.Nodes[0]; !n.Unknown || n.Report != nil {
		t.Fatalf("old-release peer = %+v, want unknown with no report", n)
	}
}

// A peer's clock may be off: its report's age comes from the peer, not from
// comparing its timestamp with this node's clock.
func TestAggregatorSnapshot_usesPeerMeasuredAge(t *testing.T) {
	skewed := &report.NodeReport{Timestamp: testNow.Add(-10 * time.Minute)} // peer clock 10m behind
	fetch := peerAgeFetcher{r: skewed, age: 3 * time.Second}
	snap, err := newAggregator(staticPeers{{ID: "p"}}, fetch, nil).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Nodes[0].Report == nil || snap.Nodes[0].ReportAgeSec != 3 {
		t.Fatalf("node = %+v, want the report kept with the peer's 3s age", snap.Nodes[0])
	}
}

type peerAgeFetcher struct {
	r   *report.NodeReport
	age time.Duration
}

func (f peerAgeFetcher) Fetch(context.Context, Peer) (PeerReport, error) {
	return PeerReport{Report: f.r, Age: f.age}, nil
}

func TestAggregatorSnapshot_cacheCountsFromCompletion(t *testing.T) {
	now := testNow
	fetch := &fakeFetcher{reports: map[string]*report.NodeReport{"p": freshReport()}}
	a := newAggregator(staticPeers{{ID: "p"}}, fetch, nil)
	a.Now = func() time.Time { return now }
	if _, err := a.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Second)
	if _, err := a.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fetch.calls.Load() != 1 {
		t.Fatalf("a snapshot 4s old was assembled again (%d fan-outs)", fetch.calls.Load())
	}
}

func TestReportAge_neverNegative(t *testing.T) {
	future := &report.NodeReport{Timestamp: testNow.Add(time.Minute)}
	if age := ReportAge(future, testNow); age != 0 {
		t.Fatalf("age = %v, want 0 for a report stamped in the future", age)
	}
}

func TestAggregatorSnapshot_unknownTooLongCountsAsUnreachable(t *testing.T) {
	now := testNow
	fetch := &fakeFetcher{errs: map[string]error{"old": ErrPeerWithoutTelemetry}}
	a := newAggregator(staticPeers{{ID: "old"}}, fetch, nil)
	a.Now = func() time.Time { return now }
	a.CacheTTL = 0
	snap, _ := a.Snapshot(context.Background())
	if !snap.Nodes[0].Unknown {
		t.Fatal("an old-release node was not shown as unknown at first")
	}
	now = now.Add(2 * time.Hour)
	snap, _ = a.Snapshot(context.Background())
	if n := snap.Nodes[0]; n.Unknown || n.Err == "" {
		t.Fatalf("after 2h the node is %+v, want it unreachable: a forgotten old node must not vanish", n)
	}
}
