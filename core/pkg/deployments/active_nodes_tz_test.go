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

// zoneClock is one instant as a clock in zone name reports it. The tests hand
// it to the manager instead of moving time.Local, which is process-wide:
// another goroutine reading it while a test wrote it failed -race.
func zoneClock(t *testing.T, name string, instant time.Time) func() time.Time {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("zone %s unavailable: %v", name, err)
	}
	return func() time.Time { return instant.In(loc) }
}

// tzInstant is the instant every test's clock reports.
var tzInstant = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

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
	db := &cutoffCaptureDB{}
	hnm := NewHomeNodeManager(db, nil, zap.NewNop())
	hnm.now = zoneClock(t, "Europe/Berlin", tzInstant)

	if _, err := hnm.getActiveNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := parseCutoff(t, db.args), tzInstant.Add(-activeNodeWindow); !got.Equal(want) {
		t.Fatalf("cutoff %v, want %v (UTC)", got, want)
	}
}

func TestGetStaleNamespaces_cutoff_is_UTC_in_zone_ahead_of_UTC(t *testing.T) {
	db := &cutoffCaptureDB{}
	hnm := NewHomeNodeManager(db, nil, zap.NewNop())
	hnm.now = zoneClock(t, "Europe/Berlin", tzInstant)

	if _, err := hnm.GetStaleNamespaces(context.Background(), time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, want := parseCutoff(t, db.args), tzInstant.Add(-time.Hour); !got.Equal(want) {
		t.Fatalf("cutoff %v, want %v (UTC)", got, want)
	}
}

func TestGetActiveNodes_cutoff_is_UTC_in_zone_behind_UTC(t *testing.T) {
	db := &cutoffCaptureDB{}
	hnm := NewHomeNodeManager(db, nil, zap.NewNop())
	hnm.now = zoneClock(t, "America/Los_Angeles", tzInstant)

	if _, err := hnm.getActiveNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := parseCutoff(t, db.args), tzInstant.Add(-activeNodeWindow); !got.Equal(want) {
		t.Fatalf("cutoff %v, want %v (UTC)", got, want)
	}
}
