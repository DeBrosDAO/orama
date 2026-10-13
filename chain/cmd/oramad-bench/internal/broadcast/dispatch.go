package broadcast

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/watch"
)

const (
	// queuePerWorker is how many transactions may wait for a worker before Enqueue blocks. The queue
	// is deep so that a briefly slow worker does not lower the offered rate; a queue that stays
	// long means the load generator, not the chain, is the bottleneck, which the report flags
	// through the longest queue seen (MaxQueueDepth).
	queuePerWorker = 8192
	// transportPrefix starts the log of a broadcast that failed before the node answered.
	transportPrefix = "transport: "
	// heldBackReason is the ledger reason for a transaction the gate kept from being sent.
	heldBackReason = "not sent: an earlier transaction of the account was refused, so its sequence has a hole"
)

// Job is one signed transaction ready to send.
type Job struct {
	// Hash identifies the transaction in the ledger (uppercase hex of its SHA-256).
	Hash string
	Raw  []byte
	// Account is the signing account's address, empty for a transaction nobody signs.
	Account string
	// Seq is the sequence the transaction was signed with.
	Seq uint64
	// Shard picks the worker and so the node: transactions of one account share a shard.
	Shard int
}

// Dispatcher sends jobs with a pool of workers. Each worker owns a node, and a shard always goes
// to the same worker, so one account's transactions reach one node in the order they were enqueued.
type Dispatcher struct {
	senders []Sender
	workers int
	gate    *account.Gate
	ledger  *watch.Ledger
	now     func() time.Time

	queues   []chan Job
	wg       sync.WaitGroup
	closed   sync.Once
	maxDepth atomic.Int64
	sent     atomic.Int64
}

// NewDispatcher returns a dispatcher over senders (one per node). The worker count is rounded up
// to a multiple of the node count so that a shard maps to one node.
func NewDispatcher(senders []Sender, workers int, gate *account.Gate, ledger *watch.Ledger, now func() time.Time) (*Dispatcher, error) {
	if len(senders) == 0 {
		return nil, errors.New("no node to send to")
	}
	if workers < 1 {
		workers = 1
	}
	if rem := workers % len(senders); rem != 0 {
		workers += len(senders) - rem
	}
	return &Dispatcher{senders: senders, workers: workers, gate: gate, ledger: ledger, now: now}, nil
}

// Start launches the workers. They run until Close.
func (d *Dispatcher) Start(ctx context.Context) {
	d.queues = make([]chan Job, d.workers)
	for w := range d.queues {
		d.queues[w] = make(chan Job, queuePerWorker)
		d.wg.Add(1)
		go d.work(ctx, d.senders[w%len(d.senders)], d.queues[w])
	}
}

// Enqueue registers the job in the ledger, stamps its start time as due, and hands it to its
// worker. due is the moment the job should have gone out, not the moment a worker got to it, so
// time spent waiting inside the load generator counts as latency.
func (d *Dispatcher) Enqueue(ctx context.Context, j Job, due time.Time) error {
	d.ledger.Register(j.Hash)
	d.ledger.Sent(j.Hash, due)
	q := d.queues[uint(j.Shard)%uint(d.workers)]
	select {
	case q <- j:
	case <-ctx.Done():
		d.ledger.NotSent(j.Hash, d.now(), "not sent: the run was cancelled")
		return ctx.Err()
	}
	d.sent.Add(1)
	depth := int64(len(q))
	for {
		cur := d.maxDepth.Load()
		if depth <= cur || d.maxDepth.CompareAndSwap(cur, depth) {
			break
		}
	}
	return nil
}

// Enqueued is how many jobs Enqueue has accepted.
func (d *Dispatcher) Enqueued() int { return int(d.sent.Load()) }

// MaxQueueDepth is the longest queue any worker had when a job joined it.
func (d *Dispatcher) MaxQueueDepth() int { return int(d.maxDepth.Load()) }

// Workers is how many workers the dispatcher runs.
func (d *Dispatcher) Workers() int { return d.workers }

// Close stops accepting jobs, lets the workers send what is queued, and waits for them.
func (d *Dispatcher) Close() {
	d.closed.Do(func() {
		for _, q := range d.queues {
			close(q)
		}
	})
	d.wg.Wait()
}

func (d *Dispatcher) work(ctx context.Context, s Sender, q <-chan Job) {
	defer d.wg.Done()
	for j := range q {
		d.send(ctx, s, j)
	}
}

func (d *Dispatcher) send(ctx context.Context, s Sender, j Job) {
	if j.Account != "" && d.gate.Blocked(j.Account, j.Seq) {
		d.ledger.NotSent(j.Hash, d.now(), heldBackReason)
		return
	}
	res, err := s.Send(ctx, j.Raw)
	switch {
	case err != nil:
		d.refuse(j, 0, transportPrefix+err.Error())
	case res.Code != 0:
		d.refuse(j, res.Code, res.Log)
	}
}

// refuse records a refusal. A transport error (code 0) is recorded the same way, although a request
// that timed out may still have reached the mempool: the account is held back either way, since
// whether its sequence has a hole is not known.
func (d *Dispatcher) refuse(j Job, code uint32, log string) {
	d.ledger.Rejected(j.Hash, d.now(), code, log)
	if j.Account != "" {
		d.gate.Fail(j.Account, j.Seq)
	}
}
