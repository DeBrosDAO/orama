package namespace

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

// Bugboard #282. getActiveNodes filters on `last_seen > ?`, and dns_nodes.last_seen
// is written with SQLite datetime('now') — UTC. The cutoff used to be formatted from
// local time, so on a node whose timezone was not Etc/UTC the comparison was wrong by
// the UTC offset. On a Europe/Berlin node the cutoff rendered ~2h AHEAD of every
// stored last_seen, so every other node was filtered out and provisioning failed with
// "insufficient nodes available for cluster" — but only when that node happened to
// serve the request, which is what made it so confusing.
//
// These tests pin the cutoff to UTC regardless of the process timezone.

// withLocalZone temporarily moves time.Local, restoring it afterwards.
func withLocalZone(t *testing.T, offsetHours int) {
	t.Helper()
	saved := time.Local
	time.Local = time.FixedZone("TEST", offsetHours*3600)
	t.Cleanup(func() { time.Local = saved })
}

// capturedCutoff runs getActiveNodes and returns the cutoff argument the query was
// issued with.
func capturedCutoff(t *testing.T) string {
	t.Helper()
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	selector := NewClusterNodeSelector(mockDB, NewNamespacePortAllocator(mockDB, logger), logger)

	if _, err := selector.getActiveNodes(context.Background()); err != nil {
		t.Fatalf("getActiveNodes: %v", err)
	}
	if len(mockDB.queryCalls) == 0 {
		t.Fatal("no query recorded")
	}
	call := mockDB.queryCalls[len(mockDB.queryCalls)-1]
	if len(call.Args) == 0 {
		t.Fatal("query issued with no cutoff argument")
	}
	s, ok := call.Args[0].(string)
	if !ok {
		t.Fatalf("cutoff arg is %T, want string", call.Args[0])
	}
	return s
}

// TestGetActiveNodes_cutoffIsUTCUnderNonUTCLocalZone is the direct reproduction: a
// process running two hours ahead of UTC must still emit a UTC cutoff.
func TestGetActiveNodes_cutoffIsUTCUnderNonUTCLocalZone(t *testing.T) {
	withLocalZone(t, +2)

	got := capturedCutoff(t)
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", got, time.UTC)
	if err != nil {
		t.Fatalf("cutoff %q not in expected layout: %v", got, err)
	}

	want := time.Now().UTC().Add(-2 * time.Minute)
	drift := parsed.Sub(want)
	if drift < 0 {
		drift = -drift
	}
	// A local-time cutoff under a +2 zone would be ~2h off; allow only clock jitter.
	if drift > 30*time.Second {
		t.Errorf("cutoff %q is %v away from UTC now-2m — the node-selection window is skewed by the local timezone", got, drift)
	}
}

// TestGetActiveNodes_cutoffSameAcrossZones pins the property that actually matters:
// the window must not depend on where the serving node thinks it is. Two nodes in
// different zones must compute the same cutoff, otherwise provisioning succeeds or
// fails depending on which node the round-robin picked.
func TestGetActiveNodes_cutoffSameAcrossZones(t *testing.T) {
	withLocalZone(t, 0)
	utcCutoff := capturedCutoff(t)

	withLocalZone(t, -7)
	westCutoff := capturedCutoff(t)

	a, err := time.ParseInLocation("2006-01-02 15:04:05", utcCutoff, time.UTC)
	if err != nil {
		t.Fatalf("parse %q: %v", utcCutoff, err)
	}
	b, err := time.ParseInLocation("2006-01-02 15:04:05", westCutoff, time.UTC)
	if err != nil {
		t.Fatalf("parse %q: %v", westCutoff, err)
	}

	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	if diff > 30*time.Second {
		t.Errorf("cutoff differs by %v between timezones (%q vs %q) — same command, different outcome depending on which node serves it", diff, utcCutoff, westCutoff)
	}
}
