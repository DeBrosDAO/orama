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
func NewCronScheduler(
	store *CronTriggerStore,
	invoker CronInvoker,
	logger *zap.Logger,
	pollInterval time.Duration,
) *CronScheduler {
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
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
		_ = s.store.MarkRun(ctx, row.TriggerID, now, now.Add(365*24*time.Hour),
			"error", "bad cron expression: "+err.Error())
		return
	}
	next, err := parsed.Next(now)
	if err != nil {
		_ = s.store.MarkRun(ctx, row.TriggerID, now, now.Add(365*24*time.Hour),
			"error", "no next match: "+err.Error())
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
	if err := s.store.MarkRun(ctx, row.TriggerID, now, next, status, errMsg); err != nil {
		s.logger.Warn("cron scheduler: MarkRun failed",
			zap.String("trigger_id", row.TriggerID),
			zap.Error(err))
	}
}
