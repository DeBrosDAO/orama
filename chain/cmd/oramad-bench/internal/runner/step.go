package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/account"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/broadcast"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/gen"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/profile"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/ratelimit"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/report"
	"github.com/DeBrosOfficial/network/chain/cmd/oramad-bench/internal/watch"
)

const (
	drainPoll  = 100 * time.Millisecond
	settlePoll = 500 * time.Millisecond
)

// errExhausted is returned when a type has no transaction left for a step: the end of its stock,
// not a failure of the run.
var errExhausted = errors.New("has no transaction left")

// stepRun is the state of one step in progress.
type stepRun struct {
	r      *Runner
	g      gen.Generator
	spec   profile.Step
	ledger *watch.Ledger
	disp   *broadcast.Dispatcher
	gate   *account.Gate
}

// runStep presigns the step's transactions, sends them, waits for them to land, and reports. from
// is the index of the type's first transaction this step uses.
func (r *Runner) runStep(ctx context.Context, g gen.Generator, spec profile.Step, from int, gas uint64) (report.Step, int, error) {
	if sa, ok := g.(gen.StepAware); ok {
		if err := sa.BeginStep(ctx, r.env); err != nil {
			return report.Step{}, 0, fmt.Errorf("refresh %s for the step: %w", g.Name(), err)
		}
	}
	if err := r.signer.reload(ctx); err != nil {
		return report.Step{}, 0, err
	}
	price, err := r.cluster.Chain.GasPrice(ctx)
	if err != nil {
		return report.Step{}, 0, err
	}
	n := min(spec.Txs(), g.Available()-from)
	if n <= 0 {
		return report.Step{}, 0, fmt.Errorf("%s: %w", g.Name(), errExhausted)
	}
	if n < spec.Txs() {
		r.warn("%s: only %d of the %d transactions this step plans are left, so it is shorter than %s", g.Name(), n, spec.Txs(), spec.Duration)
	}
	jobs, err := r.signer.presign(g, from, n, gas, price)
	if err != nil {
		return report.Step{}, 0, err
	}
	s := &stepRun{r: r, g: g, spec: spec, ledger: watch.NewLedger(), gate: account.NewGate()}
	step, err := s.send(ctx, jobs)
	return step, n, err
}

// send runs the step's load and builds its report.
func (s *stepRun) send(ctx context.Context, jobs []broadcast.Job) (report.Step, error) {
	r := s.r
	disp, err := broadcast.NewDispatcher(r.senders, r.cfg.Workers, s.gate, s.ledger, time.Now)
	if err != nil {
		return report.Step{}, err
	}
	s.disp = disp
	r.follower.SetLedger(s.ledger)
	defer r.follower.SetLedger(nil)
	disp.Start(ctx)
	start := time.Now()
	offered, sendErr := s.drive(ctx, jobs)
	end := sendEnd(s.spec, start, offered, time.Now())
	disp.Close()
	s.drain(ctx)
	if sendErr != nil && ctx.Err() == nil {
		return report.Step{}, sendErr
	}
	return s.build(start, end)
}

// drive offers the jobs in the step's mode and returns how many it offered.
func (s *stepRun) drive(ctx context.Context, jobs []broadcast.Job) (int, error) {
	if s.spec.Mode == report.ModeOpen {
		return s.disp.Open(ctx, jobs, s.spec.Rate, s.spec.Duration, ratelimit.SystemClock{})
	}
	return s.disp.Closed(ctx, jobs, s.spec.Window, s.spec.Duration, ratelimit.SystemClock{})
}

// drain waits until every transaction has an outcome or the drain time is up.
func (s *stepRun) drain(ctx context.Context) {
	deadline := time.NewTimer(s.r.cfg.Drain)
	defer deadline.Stop()
	for s.ledger.Pending() > 0 {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-s.ledger.Resolved():
		case <-time.After(drainPoll):
		}
	}
}

func (s *stepRun) build(start, sendEnd time.Time) (report.Step, error) {
	r := s.r
	now := time.Now()
	fees := r.fees.Between(start, now)
	if before, ok := r.fees.LastBefore(start); ok {
		fees = append([]watch.FeeSample{before}, fees...)
	}
	in := report.StepInput{
		Mode: s.spec.Mode, Rate: s.spec.Rate, Window: s.spec.Window, Start: start, SendEnd: sendEnd,
		Records: s.ledger.Snapshot(), Blocks: r.follower.Blocks(start, now), Mempool: r.sampler.Between(start, now),
		Fees: fees, QueueMax: s.disp.MaxQueueDepth(), BlockMaxGas: r.blockMaxGas,
	}
	step, err := report.BuildStep(in)
	if err != nil {
		return report.Step{}, fmt.Errorf("compute the step's numbers: %w", err)
	}
	if s.gate.Failures() > 0 {
		r.warn("%s: %d accounts had a transaction refused, so their later transactions of the step were not sent", s.g.Name(), s.gate.Failures())
	}
	return step, nil
}

// settle waits for every node's mempool to empty so one step's leftovers do not load the next.
func (r *Runner) settle(ctx context.Context) {
	deadline := time.NewTimer(r.cfg.Settle)
	defer deadline.Stop()
	tick := time.NewTicker(settlePoll)
	defer tick.Stop()
	for !r.mempoolsEmpty(ctx) {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			r.warn("the mempools were not empty %s after a step; the next step started with a backlog", r.cfg.Settle)
			return
		case <-tick.C:
		}
	}
}

func (r *Runner) mempoolsEmpty(ctx context.Context) bool {
	for i, v := range r.views {
		n, _, err := v.Unconfirmed(ctx)
		if err != nil {
			r.cfg.logf("cannot read the mempool of %s: %v", r.cluster.Addrs[i], err)
			return false
		}
		if n > 0 {
			return false
		}
	}
	return true
}
