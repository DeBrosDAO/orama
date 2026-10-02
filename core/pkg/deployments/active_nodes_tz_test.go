package deployments

import (
	"context"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// cutoffCaptureDB records the time-bound argument of the query it receives.
type cutoffCaptureDB struct {
	rqlite.Client
	args []any
}

func (c *cutoffCaptureDB) Query(_ context.Context, _ any, _ string, args ...any) error {
	c.args = args
	return nil
}

// withZone runs the test with the process-local zone set to name.
func withZone(t *testing.T, name string) {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("zone %s unavailable: %v", name, err)
	}
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
}

func parseCutoff(t *testing.T, args []any) time.Time {
	t.Helper()
	if len(args) != 1 {
		t.Fatalf("want one cutoff argument, got %v", args)
	}
	s, ok := args[0].(string)
	if !ok {
		t.Fatalf("cutoff is %T, want string", args[0])
	}
	got, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	if err != nil {
		t.Fatalf("cutoff %q: %v", s, err)
	}
	return got
}

// dns_nodes.last_seen is UTC text, so the cutoff must be UTC wall time on a
// node whose zone is ahead of UTC; a local cutoff filtered out every node and
// no replica could be placed (stagenet nodes run Europe/Berlin).
func TestGetActiveNodes_cutoff_is_UTC_in_zone_ahead_of_UTC(t *testing.T) {
	withZone(t, "Europe/Berlin")
	db := &cutoffCaptureDB{}
	hnm := NewHomeNodeManager(db, nil, zap.NewNop())

	if _, err := hnm.getActiveNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := time.Now().UTC().Add(-activeNodeWindow)
	if d := parseCutoff(t, db.args).Sub(want); d < -5*time.Second || d > 5*time.Second {
		t.Fatalf("cutoff is %v off the UTC expectation", d)
	}
}

func TestGetStaleNamespaces_cutoff_is_UTC_in_zone_ahead_of_UTC(t *testing.T) {
	withZone(t, "Europe/Berlin")
	db := &cutoffCaptureDB{}
	hnm := NewHomeNodeManager(db, nil, zap.NewNop())

	if _, err := hnm.GetStaleNamespaces(context.Background(), time.Hour); err != nil {
		t.Fatal(err)
	}
	want := time.Now().UTC().Add(-time.Hour)
	if d := parseCutoff(t, db.args).Sub(want); d < -5*time.Second || d > 5*time.Second {
		t.Fatalf("cutoff is %v off the UTC expectation", d)
	}
}

func TestGetActiveNodes_cutoff_is_UTC_in_zone_behind_UTC(t *testing.T) {
	withZone(t, "America/Los_Angeles")
	db := &cutoffCaptureDB{}
	hnm := NewHomeNodeManager(db, nil, zap.NewNop())

	if _, err := hnm.getActiveNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := time.Now().UTC().Add(-activeNodeWindow)
	if d := parseCutoff(t, db.args).Sub(want); d < -5*time.Second || d > 5*time.Second {
		t.Fatalf("cutoff is %v off the UTC expectation", d)
	}
}
