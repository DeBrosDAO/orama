// Package aggregator buffers PubSub trigger events per
// (namespace, function, trigger) and flushes them as a single batched
// invocation. It's used by the PubSub trigger dispatcher when a trigger
// declares aggregation_window_ms > 0.
//
// State is local to each node — buffers are not replicated. This is by
// design: aggregation is intended for high-frequency, lossy event streams
// (presence, VAD signals, metrics). Crash recovery is not provided; an
// orderly shutdown flushes pending buffers via Shutdown().
package aggregator

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"go.uber.org/zap"
)

// DefaultMaxBatchSize is used when a trigger sets MaxBatchSize=0.
const DefaultMaxBatchSize = 100

// Event is one buffered message, mirroring the dispatcher's PubSubEvent
// shape but kept local to avoid an import cycle.
type Event struct {
	Topic        string          `json:"topic"`
	Data         json.RawMessage `json:"data"`
	Namespace    string          `json:"namespace"`
	TriggerDepth int             `json:"trigger_depth"`
	Timestamp    int64           `json:"timestamp"`
}

// BatchedPayload is what the function receives on a flush.
// `Batched: true` lets a function differentiate single vs. batched mode
// by parsing this discriminator first.
type BatchedPayload struct {
	Batched bool    `json:"batched"`
	Events  []Event `json:"events"`
}

// FlushFn is invoked when a buffer flushes. It receives the marshalled
// BatchedPayload and a context with a sane timeout. The aggregator does
// not retry on flush errors — that's the invoker's responsibility.
type FlushFn func(ctx context.Context, payload []byte)

// FlushReason annotates why a flush happened. Useful for metrics.
type FlushReason string

const (
	FlushReasonTimer    FlushReason = "timer"
	FlushReasonSize     FlushReason = "size"
	FlushReasonShutdown FlushReason = "shutdown"
)

// FlushFnWithReason is like FlushFn but also receives the reason.
// Internal use; FlushFn is the simple public form.
type FlushFnWithReason func(ctx context.Context, payload []byte, reason FlushReason)

// bufferKey identifies a single in-memory buffer.
type bufferKey struct {
	Namespace  string
	FunctionID string
	TriggerID  string
}

type bufferEntry struct {
	events   []Event
	timer    *time.Timer
	windowMs int
	maxBatch int
	flushFn  FlushFnWithReason
}

// Aggregator buffers events per (namespace, function, trigger) and flushes
// either when the window timer fires or when MaxBatch is reached.
type Aggregator struct {
	mu           sync.Mutex
	buffers      map[bufferKey]*bufferEntry
	logger       *zap.Logger
	flushTimeout time.Duration
	// inflight tracks dispatched flush goroutines so Shutdown can wait
	// for them to finish (or time out) before returning.
	inflight sync.WaitGroup
}

// New creates an Aggregator. flushTimeout bounds the context passed to FlushFn.
// 0 selects a sane default (60s).
func New(logger *zap.Logger, flushTimeout time.Duration) *Aggregator {
	if flushTimeout <= 0 {
		flushTimeout = 60 * time.Second
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Aggregator{
		buffers:      map[bufferKey]*bufferEntry{},
		logger:       logger.Named("aggregator"),
		flushTimeout: flushTimeout,
	}
}

// BufferRequest carries everything needed to add an event.
type BufferRequest struct {
	Namespace    string
	FunctionID   string
	TriggerID    string
	WindowMs     int
	MaxBatchSize int
	FlushFn      FlushFn // simple public form; internally promoted to FlushFnWithReason
	Event        Event
}

// Buffer adds an event to the matching buffer. Returns immediately —
// the function is invoked later, asynchronously, when the window or
// size threshold fires.
//
// If WindowMs <= 0, this method panics with a programming-error message
// to surface misuse: callers should not buffer non-aggregating triggers.
func (a *Aggregator) Buffer(req BufferRequest) {
	if req.WindowMs <= 0 {
		// Aggregator should never be called for non-aggregating triggers.
		// Panicking here makes the caller bug obvious during development.
		panic("aggregator: Buffer called with WindowMs <= 0")
	}
	maxBatch := req.MaxBatchSize
	if maxBatch <= 0 {
		maxBatch = DefaultMaxBatchSize
	}

	key := bufferKey{Namespace: req.Namespace, FunctionID: req.FunctionID, TriggerID: req.TriggerID}

	a.mu.Lock()
	defer a.mu.Unlock()

	entry, ok := a.buffers[key]
	if !ok {
		// Promote the user-facing FlushFn into the reason-aware variant.
		// We capture req.FlushFn so subsequent Buffer calls keep using it.
		userFn := req.FlushFn
		entry = &bufferEntry{
			events:   make([]Event, 0, maxBatch),
			windowMs: req.WindowMs,
			maxBatch: maxBatch,
			flushFn: func(ctx context.Context, payload []byte, reason FlushReason) {
				if userFn != nil {
					userFn(ctx, payload)
				}
			},
		}
		a.buffers[key] = entry
	}

	entry.events = append(entry.events, req.Event)

	// Start the window timer on the first event of a new window.
	if entry.timer == nil {
		// Capture key by value for the closure.
		k := key
		entry.timer = time.AfterFunc(time.Duration(entry.windowMs)*time.Millisecond, func() {
			a.flushByTimer(k)
		})
	}

	// Size-triggered flush.
	if len(entry.events) >= entry.maxBatch {
		a.flushLocked(key, entry, FlushReasonSize)
	}
}

// flushByTimer is invoked by time.AfterFunc; it acquires the lock then flushes.
func (a *Aggregator) flushByTimer(key bufferKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.buffers[key]
	if !ok {
		// Buffer already flushed by size threshold and the bucket removed.
		return
	}
	if len(entry.events) == 0 {
		// Defensive: empty buffer — drop it so the map stays bounded.
		delete(a.buffers, key)
		return
	}
	a.flushLocked(key, entry, FlushReasonTimer)
}

// flushLocked must be called with a.mu held. It snapshots the current
// events, removes the bucket entry, then dispatches the flush in a
// goroutine so the caller doesn't block on the function invocation.
//
// Removing the bucket on flush keeps the buffers map bounded over the
// lifetime of the process. If a subsequent event arrives for the same
// (namespace, function, trigger) tuple, Buffer recreates the entry.
func (a *Aggregator) flushLocked(key bufferKey, entry *bufferEntry, reason FlushReason) {
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	if len(entry.events) == 0 {
		// Empty bucket — drop it so the map doesn't accumulate.
		delete(a.buffers, key)
		return
	}
	events := entry.events

	payload, err := json.Marshal(BatchedPayload{Batched: true, Events: events})
	if err != nil {
		a.logger.Error("failed to marshal batched payload",
			zap.String("namespace", key.Namespace),
			zap.String("function_id", key.FunctionID),
			zap.String("trigger_id", key.TriggerID),
			zap.Int("batch_size", len(events)),
			zap.Error(err),
		)
		// Still drop the bucket — there's no point retrying with the same data.
		delete(a.buffers, key)
		return
	}

	a.logger.Debug("aggregator flush",
		zap.String("namespace", key.Namespace),
		zap.String("function_id", key.FunctionID),
		zap.String("trigger_id", key.TriggerID),
		zap.Int("batch_size", len(events)),
		zap.String("reason", string(reason)),
	)

	flushFn := entry.flushFn
	timeout := a.flushTimeout
	delete(a.buffers, key)

	a.inflight.Add(1)
	go func() {
		defer a.inflight.Done()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		flushFn(ctx, payload, reason)
	}()
}

// Shutdown drains all non-empty buffers and waits for the resulting flush
// invocations to finish, bounded by `wait`. Callers should pass a wait
// long enough to cover one function invocation (e.g. 5–10 seconds) but
// short enough that a misbehaving function can't delay process exit.
//
// Returns true if all in-flight flushes completed before the deadline,
// false on timeout (in which case some events are effectively lost).
func (a *Aggregator) Shutdown(wait time.Duration) bool {
	a.mu.Lock()
	keys := make([]bufferKey, 0, len(a.buffers))
	for k := range a.buffers {
		keys = append(keys, k)
	}
	for _, k := range keys {
		entry := a.buffers[k]
		if entry == nil {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
			entry.timer = nil
		}
		a.flushLocked(k, entry, FlushReasonShutdown)
	}
	a.mu.Unlock()

	if wait <= 0 {
		return true
	}
	done := make(chan struct{})
	go func() {
		a.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(wait):
		a.logger.Warn("aggregator shutdown timed out; some buffered events may be lost")
		return false
	}
}

// Stats reports the current number of buffered events across all keys.
// Useful for metrics.
func (a *Aggregator) Stats() (numBuffers, totalEvents int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	numBuffers = len(a.buffers)
	for _, e := range a.buffers {
		totalEvents += len(e.events)
	}
	return
}
