//go:build e2e_fleet

package realistic

import (
	"context"
	"testing"
	"time"
)

// TestPaced_holdsTheRate: 2 workers at a 50ms interval never exceed 40 calls
// a second, where Burst would run them back to back.
func TestPaced_holdsTheRate(t *testing.T) {
	const workers, total = 2, 8
	interval := 50 * time.Millisecond
	start := time.Now()
	got := Paced(context.Background(), workers, total, interval, func(context.Context, int, int) error { return nil })
	if len(got) != total {
		t.Fatalf("got %d samples, want %d", len(got), total)
	}
	// 4 calls per worker, each starting 50ms after the last: 3 gaps.
	if min := 3 * interval; time.Since(start) < min {
		t.Errorf("finished in %s, want at least %s", time.Since(start), min)
	}
}

func TestPaced_stopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// The first call cancels: Paced must then return instead of waiting out
	// the hour-long interval (the test binary's timeout fails it otherwise).
	got := Paced(ctx, 1, 1000, time.Hour, func(context.Context, int, int) error { cancel(); return nil })
	if len(got) != 1 {
		t.Errorf("got %d samples, want 1", len(got))
	}
}
