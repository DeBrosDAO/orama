package broadcast

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/watch"
)

var epoch = time.Unix(1_700_000_000, 0)

// stepClock only moves when slept on, so pacing needs no real time.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

// recorder is a Sender that remembers what reached it, per node.
type recorder struct {
	mu    sync.Mutex
	got   []string
	reply func(raw []byte) (CheckResult, error)
}

func (r *recorder) Send(_ context.Context, raw []byte) (CheckResult, error) {
	r.mu.Lock()
	r.got = append(r.got, string(raw))
	r.mu.Unlock()
	if r.reply != nil {
		return r.reply(raw)
	}
	return CheckResult{}, nil
}

func (r *recorder) received() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

func job(account string, seq uint64, shard int) Job {
	name := fmt.Sprintf("%s-%d", account, seq)
	return Job{Hash: name, Raw: []byte(name), Account: account, Seq: seq, Shard: shard}
}

func newDispatcher(t *testing.T, senders []Sender, workers int) (*Dispatcher, *watch.Ledger, *account.Gate) {
	t.Helper()
	ledger, gate := watch.NewLedger(), account.NewGate()
	d, err := NewDispatcher(senders, workers, gate, ledger, func() time.Time { return epoch })
	if err != nil {
		t.Fatal(err)
	}
	return d, ledger, gate
}

func TestDispatcher_anAccountsTransactionsReachOneNodeInOrder(t *testing.T) {
	a, b := &recorder{}, &recorder{}
	d, _, _ := newDispatcher(t, []Sender{a, b}, 4)
	ctx := context.Background()
	d.Start(ctx)
	var jobs []Job
	for seq := uint64(0); seq < 50; seq++ {
		for shard := 0; shard < 6; shard++ {
			jobs = append(jobs, job(fmt.Sprintf("acct%d", shard), seq, shard))
		}
	}
	for _, j := range jobs {
		if err := d.Enqueue(ctx, j, epoch); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()
	for shard := 0; shard < 6; shard++ {
		holders := 0
		for _, rec := range []*recorder{a, b} {
			last := -1
			count := 0
			for _, raw := range rec.received() {
				var acct string
				var seq int
				fmt.Sscanf(raw, "acct%1s-%d", &acct, &seq)
				if acct != fmt.Sprint(shard) {
					continue
				}
				count++
				if seq <= last {
					t.Fatalf("account %d: sequence %d arrived after %d", shard, seq, last)
				}
				last = seq
			}
			if count > 0 {
				holders++
				if count != 50 {
					t.Fatalf("account %d: node got %d of its 50 transactions", shard, count)
				}
			}
		}
		if holders != 1 {
			t.Fatalf("account %d reached %d nodes, want exactly 1", shard, holders)
		}
	}
	if d.Enqueued() != 300 {
		t.Fatalf("enqueued %d", d.Enqueued())
	}
}

func TestDispatcher_workerCountRoundsUpToWholeNodes(t *testing.T) {
	d, _, _ := newDispatcher(t, []Sender{&recorder{}, &recorder{}, &recorder{}}, 4)
	if d.Workers() != 6 {
		t.Fatalf("workers %d", d.Workers())
	}
	if _, err := NewDispatcher(nil, 1, account.NewGate(), watch.NewLedger(), time.Now); err == nil {
		t.Fatal("a dispatcher with no node was built")
	}
	d1, _, _ := newDispatcher(t, []Sender{&recorder{}}, 0)
	if d1.Workers() != 1 {
		t.Fatalf("zero workers became %d", d1.Workers())
	}
}

func TestDispatcher_aRefusalIsRecordedAndHoldsBackTheAccountsLaterTransactions(t *testing.T) {
	rec := &recorder{reply: func(raw []byte) (CheckResult, error) {
		if string(raw) == "a-2" {
			return CheckResult{Code: 19, Log: "insufficient fee"}, nil
		}
		return CheckResult{}, nil
	}}
	d, ledger, gate := newDispatcher(t, []Sender{rec}, 1)
	ctx := context.Background()
	d.Start(ctx)
	for seq := uint64(0); seq < 5; seq++ {
		if err := d.Enqueue(ctx, job("a", seq, 0), epoch); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Enqueue(ctx, job("b", 0, 0), epoch); err != nil {
		t.Fatal(err)
	}
	d.Close()
	snap := ledger.Snapshot()
	if snap["a-2"].Outcome != watch.Rejected || snap["a-2"].Code != 19 {
		t.Fatalf("a-2: %+v", snap["a-2"])
	}
	for _, h := range []string{"a-3", "a-4"} {
		if snap[h].Outcome != watch.NotSent {
			t.Fatalf("%s: %+v", h, snap[h])
		}
	}
	if got := rec.received(); len(got) != 4 {
		t.Fatalf("the node received %v, want a-0 a-1 a-2 b-0", got)
	}
	if gate.Failures() != 1 {
		t.Fatalf("gate failures %d", gate.Failures())
	}
}

func TestDispatcher_closeTwiceIsSafeAndACancelledEnqueueLeavesNothingPending(t *testing.T) {
	d, ledger, _ := newDispatcher(t, []Sender{&recorder{}}, 1)
	// No worker reads this queue and it holds nothing, so Enqueue waits until its context ends.
	d.queues = []chan Job{make(chan Job)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := d.Enqueue(ctx, job("a", 0, 0), epoch)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if r := ledger.Snapshot()["a-0"]; r.Outcome != watch.NotSent || ledger.Pending() != 0 {
		t.Fatalf("a job that was never queued must not stay pending: %+v", r)
	}
	d.Close()
	d.Close()
}

func TestDispatcher_aTransportErrorIsARejectionNotACrash(t *testing.T) {
	rec := &recorder{reply: func([]byte) (CheckResult, error) { return CheckResult{}, errors.New("connection refused") }}
	d, ledger, _ := newDispatcher(t, []Sender{rec}, 1)
	d.Start(context.Background())
	if err := d.Enqueue(context.Background(), job("a", 0, 0), epoch); err != nil {
		t.Fatal(err)
	}
	d.Close()
	r := ledger.Snapshot()["a-0"]
	if r.Outcome != watch.Rejected || r.Log != "transport: connection refused" {
		t.Fatalf("record %+v", r)
	}
}

func TestDispatcher_signerlessJobsAreNeverHeldBack(t *testing.T) {
	rec := &recorder{}
	d, ledger, gate := newDispatcher(t, []Sender{rec}, 1)
	gate.Fail("", 0)
	d.Start(context.Background())
	for i := 0; i < 3; i++ {
		j := Job{Hash: fmt.Sprintf("s%d", i), Raw: []byte("x"), Shard: i}
		if err := d.Enqueue(context.Background(), j, epoch); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()
	if len(rec.received()) != 3 || ledger.Pending() != 3 {
		t.Fatalf("received %d, pending %d", len(rec.received()), ledger.Pending())
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]Mode{"sync": ModeSync, "ASYNC": ModeAsync} {
		got, err := ParseMode(in)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseMode("fire"); err == nil {
		t.Error("an unknown mode was accepted")
	}
}
