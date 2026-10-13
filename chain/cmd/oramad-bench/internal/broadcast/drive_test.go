package broadcast

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/watch"
)

func TestOpen_offersTheRateForTheDuration(t *testing.T) {
	rec := &recorder{}
	d, ledger, _ := newDispatcher(t, []Sender{rec}, 2)
	ctx, clock := context.Background(), &stepClock{now: epoch}
	d.Start(ctx)
	jobs := make([]Job, 1000)
	for i := range jobs {
		jobs[i] = job("a", uint64(i), 0)
	}
	offered, err := d.Open(ctx, jobs, 50, 2*time.Second, clock)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if offered != 100 {
		t.Fatalf("offered %d, want 50/s for 2s = 100", offered)
	}
	if ledger.Snapshot()["a-99"].SentAt.Sub(epoch) != 1980*time.Millisecond {
		t.Fatalf("the last job was stamped %s after the start", ledger.Snapshot()["a-99"].SentAt.Sub(epoch))
	}
}

func TestOpen_stopsWhenTheJobsRunOutAndRejectsABadRate(t *testing.T) {
	d, _, _ := newDispatcher(t, []Sender{&recorder{}}, 1)
	ctx, clock := context.Background(), &stepClock{now: epoch}
	d.Start(ctx)
	defer d.Close()
	offered, err := d.Open(ctx, []Job{job("a", 0, 0), job("a", 1, 0)}, 100, time.Hour, clock)
	if err != nil || offered != 2 {
		t.Fatalf("offered %d, %v", offered, err)
	}
	if _, err := d.Open(ctx, nil, 0, time.Second, clock); err == nil {
		t.Fatal("rate 0 accepted")
	}
}

func TestClosed_neverExceedsTheWindow(t *testing.T) {
	var inflight, peak atomic.Int64
	rec := &recorder{}
	ledger, gate := watch.NewLedger(), account.NewGate()
	d, err := NewDispatcher([]Sender{rec}, 1, gate, ledger, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	rec.reply = func(raw []byte) (CheckResult, error) {
		cur := inflight.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		// The chain "includes" the transaction a moment later.
		go func(h string) {
			time.Sleep(2 * time.Millisecond)
			inflight.Add(-1)
			ledger.Included(h, time.Now(), 1, 0, "")
		}(string(raw))
		return CheckResult{}, nil
	}
	ctx := context.Background()
	d.Start(ctx)
	jobs := make([]Job, 60)
	for i := range jobs {
		jobs[i] = job("a", uint64(i), 0)
	}
	offered, err := d.Closed(ctx, jobs, 5, 5*time.Second, ratelimitSystem{})
	d.Close()
	if err != nil || offered != 60 {
		t.Fatalf("offered %d, %v", offered, err)
	}
	if peak.Load() > 5 {
		t.Fatalf("%d transactions were in flight with a window of 5", peak.Load())
	}
}

func TestClosed_stopsAtTheDurationWhenNothingCompletes(t *testing.T) {
	d, _, _ := newDispatcher(t, []Sender{&recorder{}}, 1)
	ctx := context.Background()
	d.Start(ctx)
	defer d.Close()
	jobs := []Job{job("a", 0, 0), job("a", 1, 0), job("a", 2, 0)}
	offered, err := d.Closed(ctx, jobs, 2, 50*time.Millisecond, ratelimitSystem{})
	if err != nil || offered != 2 {
		t.Fatalf("offered %d, %v: nothing completed, so the window of 2 must hold", offered, err)
	}
	if _, err := d.Closed(ctx, jobs, 0, time.Second, ratelimitSystem{}); err == nil {
		t.Fatal("window 0 accepted")
	}
}
