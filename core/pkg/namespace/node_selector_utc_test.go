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

// cutoffLayout is how the cutoff is rendered for the string comparison.
const cutoffLayout = "2006-01-02 15:04:05"

// zoned is one instant as a clock in a zone offsetHours from UTC would report
// it. The tests hand it to the selector instead of moving time.Local, which is
// process-wide: a goroutine still running from another test read it while one
// of these wrote it, and -race failed the package.
func zoned(instant time.Time, offsetHours int) func() time.Time {
	return func() time.Time { return instant.In(time.FixedZone("TEST", offsetHours*3600)) }
}

// capturedCutoff runs getActiveNodes on clock now and returns the cutoff argument
// the query was issued with.
func capturedCutoff(t *testing.T, now func() time.Time) string {
	t.Helper()
	logger := zap.NewNop()
	mockDB := newMockRQLiteClient()
	selector := NewClusterNodeSelector(mockDB, NewNamespacePortAllocator(mockDB, logger), logger)
	selector.now = now

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
// clock two hours ahead of UTC must still yield a UTC cutoff.
func TestGetActiveNodes_cutoffIsUTCUnderNonUTCLocalZone(t *testing.T) {
	instant := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	got := capturedCutoff(t, zoned(instant, +2))
	if want := instant.Add(-2 * time.Minute).Format(cutoffLayout); got != want {
		t.Errorf("cutoff %q, want %q: the node-selection window is skewed by the clock's zone", got, want)
	}
}

// TestGetActiveNodes_cutoffSameAcrossZones pins the property that actually matters:
// the window must not depend on where the serving node thinks it is. Two nodes in
// different zones must compute the same cutoff, otherwise provisioning succeeds or
// fails depending on which node the round-robin picked.
func TestGetActiveNodes_cutoffSameAcrossZones(t *testing.T) {
	instant := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	utc := capturedCutoff(t, zoned(instant, 0))
	west := capturedCutoff(t, zoned(instant, -7))
	if utc != west {
		t.Errorf("cutoff differs between zones (%q vs %q) — same command, different outcome depending on which node serves it", utc, west)
	}
}
