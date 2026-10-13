package hub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func healthySnapshot(nodes ...cluster.CollectionStatus) func(context.Context) (*cluster.ClusterSnapshot, error) {
	return func(context.Context) (*cluster.ClusterSnapshot, error) {
		return &cluster.ClusterSnapshot{Nodes: nodes}, nil
	}
}

func okNode() cluster.CollectionStatus {
	return cluster.CollectionStatus{Report: &report.NodeReport{
		Gateway: &report.GatewayReport{Responsive: true, HTTPStatus: 200},
	}}
}

func countRows(t *testing.T, u UptimeStore) int {
	t.Helper()
	var n int
	if err := u.DB.QueryRow(`SELECT COUNT(*) FROM status_uptime_hourly`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordOnce_onlyLowestActiveWrites(t *testing.T) {
	store := UptimeStore{DB: openUptimeDB(t)}
	peers := staticPeers{{ID: "a", Status: "active"}, {ID: "b", Status: "active"}}
	at := time.Date(2026, 9, 27, 10, 5, 0, 0, time.UTC)
	for _, self := range []string{"b", "a"} {
		u := &UptimeRecorder{SelfID: self, Snapshot: healthySnapshot(okNode()), Peers: peers, Store: store, Now: func() time.Time { return at }}
		if err := u.recordOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if self == "b" && countRows(t, store) != 0 {
			t.Fatal("a node that is not the lowest active id wrote uptime")
		}
	}
	if countRows(t, store) == 0 {
		t.Fatal("the lowest active node did not write uptime")
	}
}

func TestRecordOnce_skipsMinuteWhileANodeIsUnknown(t *testing.T) {
	store := UptimeStore{DB: openUptimeDB(t)}
	u := &UptimeRecorder{
		SelfID: "a", Peers: staticPeers{{ID: "a", Status: "active"}}, Store: store,
		Snapshot: healthySnapshot(okNode(), cluster.CollectionStatus{Unknown: true}),
	}
	if err := u.recordOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if countRows(t, store) != 0 {
		t.Fatal("uptime recorded mid-rollout, while a node's state was unknown")
	}
}

func TestRecordOnce_snapshotErrorIsReturned(t *testing.T) {
	u := &UptimeRecorder{
		SelfID: "a", Peers: staticPeers{{ID: "a", Status: "active"}}, Store: UptimeStore{DB: openUptimeDB(t)},
		Snapshot: func(context.Context) (*cluster.ClusterSnapshot, error) { return nil, errors.New("registry down") },
	}
	if err := u.recordOnce(context.Background()); err == nil {
		t.Fatal("a failed snapshot was not reported")
	}
}

func TestRecordSafely_panicBecomesError(t *testing.T) {
	u := &UptimeRecorder{
		SelfID: "a", Peers: staticPeers{{ID: "a", Status: "active"}},
		Snapshot: func(context.Context) (*cluster.ClusterSnapshot, error) { panic("driver bug") },
	}
	if err := u.recordSafely(context.Background()); err == nil {
		t.Fatal("a panicking sample did not come back as an error")
	}
}
