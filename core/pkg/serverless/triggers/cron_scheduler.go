package triggers

import (
	"context"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// CronInvoker is the subset of the gateway's serverless.Invoker that the
// scheduler uses. Stated as an interface so tests can swap it out.
type CronInvoker interface {
	Invoke(ctx context.Context, req *serverless.InvokeRequest) (*serverless.InvokeResponse, error)
}

// CronScheduler is a single goroutine that periodically scans the
// function_cron_triggers table for due rows and invokes the matching
// functions.
//
// One scheduler per gateway instance is sufficient: the work is bounded
// by the number of cron triggers (small) and per-tick polling is cheap.
// If multiple gateway instances run concurrently, MarkRun's "next_run_at
// = computed_next" UPDATE acts as a soft lease — duplicate invocations
// are reduced to a tight race window. For stricter exactly-once we'd
// need an explicit lease table; deferred until needed.
type CronScheduler struct {
	store        *CronTriggerStore
	invoker      CronInvoker
	logger       *zap.Logger
	pollInterval time.Duration
	batchLimit   int

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewCronScheduler builds a scheduler. Reasonable defaults: poll every
// 30 seconds, dispatch up to 100 triggers per tick.
//
// Sub-second pollInterval is permitted (down to the engine config's
// MinCronPollInterval) for typing/presence-style ephemeral state prune
// workloads — see bugboard #109. Each tick costs ~1 rqlite ListDue
// + ~2 MarkRun writes per dispatched trigger (per-call ~340-450ms on
// a cross-region cluster), so picking faster than that on average
// queues ticks. Logged as a warning when the operator goes below 1s
// so the trade-off is visible.
func NewCronScheduler(
	store *CronTriggerStore,
	invoker CronInvoker,
	logger *zap.Logger,
	pollInterval time.Duration,
) *CronScheduler {
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}
	if pollInterval < time.Second {
		logger.Warn("cron scheduler: sub-second poll interval; ensure per-tick rqlite cost is bounded or scheduler will queue ticks indefinitely (bugboard #109)",
			zap.Duration("poll_interval", pollInterval))
	}
	return &CronScheduler{
		store:        store,
		invoker:      invoker,
		logger:       logger,
		pollInterval: pollInterval,
		batchLimit:   100,
	}
}

// Start launches the polling goroutine. Idempotent: a second Start while
// already running is a no-op.
func (s *CronScheduler) Start(ctx context.Context) {
	if s.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(1)
	go s.loop(runCtx)
	s.logger.Info("cron scheduler started", zap.Duration("poll_interval", s.pollInterval))
}

// Stop cancels the goroutine and waits for it to exit. Safe to call
// multiple times.
func (s *CronScheduler) Stop() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	s.cancel = nil
	s.wg.Wait()
	s.logger.Info("cron scheduler stopped")
}

func (s *CronScheduler) loop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	// Run an immediate tick so a freshly-booted gateway picks up triggers
	// that fired during the downtime, instead of waiting `pollInterval`.
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *CronScheduler) tick(ctx context.Context) {
	now := time.Now().UTC()
	due, err := s.store.ListDue(ctx, now, s.batchLimit)
	if err != nil {
		s.logger.Warn("cron scheduler: ListDue failed", zap.Error(err))
		return
	}
	if len(due) == 0 {
		return
	}
	for _, row := range due {
		if ctx.Err() != nil {
			return
		}
		s.dispatch(ctx, row, now)
	}
}

func (s *CronScheduler) dispatch(ctx context.Context, row CronDueRow, now time.Time) {
	// Compute the next run BEFORE invoking so we always advance the cursor
	// even if the invoke errors. This prevents a busted handler from
	// starving the scheduler.
	parsed, err := ParseCron(row.CronExpression)
	if err != nil {
		s.logger.Warn("cron scheduler: bad expression — disabling trigger",
			zap.String("trigger_id", row.TriggerID),
			zap.String("expr", row.CronExpression),
			zap.Error(err))
		// Push next_run_at far out so we don't keep looking at this row.
		// Best-effort; if the lease is lost (ErrAlreadyRan) another gateway
		// already saw the same bad expression and is handling it.
		_ = s.store.MarkRun(ctx, row.TriggerID, row.NextRunAt, now, now.Add(365*24*time.Hour),
			"error", "bad cron expression: "+err.Error())
		return
	}
	next, err := parsed.Next(now)
	if err != nil {
		_ = s.store.MarkRun(ctx, row.TriggerID, row.NextRunAt, now, now.Add(365*24*time.Hour),
			"error", "no next match: "+err.Error())
		return
	}

	// Claim the lease BEFORE invoking. The compare-and-swap on next_run_at
	// ensures only one gateway wins the race when multiple instances see
	// the same trigger as due. Pre-emptive claim (advance to the new next
	// + provisional "running" status) means we don't issue duplicate
	// invokes; the loser bails here. We patch in the real status after the
	// invoke completes via a follow-up unconditional UPDATE keyed on the
	// new next_run_at — the loser can't accidentally clobber that because
	// its expected-old next_run_at no longer matches anything.
	if err := s.store.MarkRun(ctx, row.TriggerID, row.NextRunAt, now, next, "running", ""); err != nil {
		if err == ErrAlreadyRan {
			// Another gateway claimed this tick. Skip silently.
			return
		}
		s.logger.Warn("cron scheduler: failed to claim lease",
			zap.String("trigger_id", row.TriggerID),
			zap.Error(err))
		return
	}

	req := &serverless.InvokeRequest{
		Namespace:    row.Namespace,
		FunctionName: row.FunctionName,
		Input:        []byte(`{"trigger":"cron"}`),
		TriggerType:  serverless.TriggerTypeCron,
	}
	resp, invErr := s.invoker.Invoke(ctx, req)
	status := "ok"
	errMsg := ""
	if invErr != nil {
		status = "error"
		errMsg = invErr.Error()
		s.logger.Warn("cron scheduler: invoke failed",
			zap.String("trigger_id", row.TriggerID),
			zap.String("function", row.FunctionName),
			zap.String("namespace", row.Namespace),
			zap.Error(invErr))
	} else if resp != nil && resp.Status != serverless.InvocationStatusSuccess {
		status = "error"
		errMsg = resp.Error
	}
	// Patch the result over the provisional "running" row. We're CAS'd on
	// the (now) post-claim next_run_at so a stale loser from another node
	// can't overwrite — though by construction, only this gateway holds
	// the lease at this point.
	if err := s.store.MarkRun(ctx, row.TriggerID, next, now, next, status, errMsg); err != nil && err != ErrAlreadyRan {
		s.logger.Warn("cron scheduler: failed to record run result",
			zap.String("trigger_id", row.TriggerID),
			zap.Error(err))
	}
}
