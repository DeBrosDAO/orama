package serverless

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// invocationLogQueueSize bounds the number of pending invocation records held
// off the reply critical path. Telemetry must never block or OOM the data
// path: once this many records are queued, new records are DROPPED (counted)
// rather than backing up onto the caller. 4096 is generous — at a sustained
// drain rate of one cross-region Raft write per record, this absorbs multi-
// second bursts before any drop occurs.
const invocationLogQueueSize = 4096

// invocationLogWriteTimeout bounds a single record's write. The request
// context that produced the record is already dead by the time the worker
// drains it (Execute returned), so the worker uses its own context with this
// per-record deadline instead.
const invocationLogWriteTimeout = 10 * time.Second

// invocationLogFlushTimeout caps how long Close waits for the worker to drain
// pending records at shutdown. Best-effort: losing telemetry at shutdown is
// acceptable, so we never block the process from exiting.
const invocationLogFlushTimeout = 5 * time.Second

// dropWarnInterval rate-limits the "queue full, dropping" WARN so a sustained
// overload doesn't itself flood the logs.
const dropWarnInterval = 30 * time.Second

// invocationLogQueue moves invocation telemetry OFF the reply critical path.
//
// Behavior note: records are now written ASYNCHRONOUSLY by a single worker
// goroutine, so a function_invocations row may lag the response by up to the
// queue drain time. That lag is acceptable for telemetry and is worth it — it
// removes ~500ms-3s of cross-region Raft write latency from every serverless
// RPC round-trip (bugboard feat-27). Each record's Logs are batched into a
// single multi-row INSERT by the logger impls, so a handler that emits N log
// lines no longer pays N sequential cross-region writes.
type invocationLogQueue struct {
	logger *zap.Logger
	sink   InvocationLogger

	ch chan *InvocationRecord
	wg sync.WaitGroup

	dropped      atomic.Int64
	lastDropWarn atomic.Int64 // unix-nano of last drop warning emitted
	closeOnce    sync.Once
}

// newInvocationLogQueue starts the single drain worker and returns the queue.
// sink is the underlying logger whose Log method performs the actual DB write;
// it is called with the worker's own context, never the request context.
func newInvocationLogQueue(sink InvocationLogger, logger *zap.Logger) *invocationLogQueue {
	q := &invocationLogQueue{
		logger: logger,
		sink:   sink,
		ch:     make(chan *InvocationRecord, invocationLogQueueSize),
	}
	q.wg.Add(1)
	go q.run()
	return q
}

// enqueue submits a record for asynchronous writing. It NEVER blocks: if the
// bounded queue is full, the record is dropped and a counter incremented, with
// a rate-limited WARN that reports the running drop count. Returns true if the
// record was accepted, false if dropped.
func (q *invocationLogQueue) enqueue(rec *InvocationRecord) bool {
	if rec == nil {
		return false
	}
	select {
	case q.ch <- rec:
		return true
	default:
		dropped := q.dropped.Add(1)
		q.maybeWarnDrop(dropped)
		return false
	}
}

// maybeWarnDrop emits a rate-limited WARN reporting the cumulative drop count.
func (q *invocationLogQueue) maybeWarnDrop(dropped int64) {
	now := time.Now().UnixNano()
	last := q.lastDropWarn.Load()
	if now-last < int64(dropWarnInterval) {
		return
	}
	if !q.lastDropWarn.CompareAndSwap(last, now) {
		return
	}
	q.logger.Warn("invocation log queue full; dropping telemetry records",
		zap.Int64("dropped_total", dropped),
		zap.Int("queue_size", invocationLogQueueSize),
	)
}

// run drains the queue, writing each record with the worker's own context and
// a per-record timeout. It exits when the channel is closed and fully drained.
func (q *invocationLogQueue) run() {
	defer q.wg.Done()
	for rec := range q.ch {
		q.write(rec)
	}
}

// write performs a single record write with a bounded, request-independent
// context. Failures are logged (never swallowed silently) but do not stop the
// worker — telemetry loss must never cascade into the data path.
func (q *invocationLogQueue) write(rec *InvocationRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), invocationLogWriteTimeout)
	defer cancel()
	if err := q.sink.Log(ctx, rec); err != nil {
		q.logger.Warn("failed to write invocation telemetry record",
			zap.String("function_id", rec.FunctionID),
			zap.String("request_id", rec.RequestID),
			zap.Error(err),
		)
	}
}

// Close stops accepting new records and waits (bounded by
// invocationLogFlushTimeout) for the worker to flush what's already queued.
// Best-effort: if the worker doesn't finish in time, Close returns anyway so
// shutdown is never blocked by telemetry. Safe to call multiple times.
func (q *invocationLogQueue) Close() {
	q.closeOnce.Do(func() {
		close(q.ch)
		flushed := make(chan struct{})
		go func() {
			q.wg.Wait()
			close(flushed)
		}()
		select {
		case <-flushed:
		case <-time.After(invocationLogFlushTimeout):
			q.logger.Warn("invocation log queue flush timed out; dropping remaining telemetry",
				zap.Duration("timeout", invocationLogFlushTimeout),
			)
		}
	})
}
