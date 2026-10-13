package ops

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestEach_runsEveryIndexOnceWithinTheParallelLimit(t *testing.T) {
	const n, limit = 50, 4
	var running, peak, ran atomic.Int64
	seen := make([]atomic.Int64, n)
	err := Each(context.Background(), n, limit, func(_ context.Context, i int) error {
		cur := running.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		seen[i].Add(1)
		ran.Add(1)
		running.Add(-1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ran.Load() != n || peak.Load() > limit {
		t.Fatalf("ran %d times, peak %d in flight (limit %d)", ran.Load(), peak.Load(), limit)
	}
	for i := range seen {
		if seen[i].Load() != 1 {
			t.Fatalf("index %d ran %d times", i, seen[i].Load())
		}
	}
}

func TestEach_returnsTheFirstErrorAndStopsStartingWork(t *testing.T) {
	boom := errors.New("boom")
	var started atomic.Int64
	err := Each(context.Background(), 1000, 1, func(_ context.Context, i int) error {
		started.Add(1)
		if i == 3 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if started.Load() > 10 {
		t.Fatalf("started %d calls after the failure", started.Load())
	}
}

func TestEach_emptyAndCancelled(t *testing.T) {
	if err := Each(context.Background(), 0, 3, nil); err != nil {
		t.Fatalf("empty: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Each(ctx, 5, 2, func(context.Context, int) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestEach_nonPositiveParallelMeansOne(t *testing.T) {
	var n atomic.Int64
	if err := Each(context.Background(), 3, 0, func(context.Context, int) error { n.Add(1); return nil }); err != nil || n.Load() != 3 {
		t.Fatalf("ran %d, %v", n.Load(), err)
	}
}
