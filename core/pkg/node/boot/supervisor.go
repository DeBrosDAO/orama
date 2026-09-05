// Package boot converges a node's services instead of starting them once, in
// order, and giving up.
//
// The node used to start as a straight line: WireGuard, libp2p, storage,
// rqlite, CoreDNS, gateway, edge. Any step returning an error aborted the
// process, systemd restarted it five seconds later, and the line began again
// from the top. That is the correct shape for a program whose dependencies are
// all local, and the wrong shape for one whose fourth step waits on a raft
// quorum: a node that boots while its peers are down never reaches step five,
// so it serves no HTTPS, no DNS, and no tenants — not because those cannot run
// without a quorum, but because nothing ever tried to start them.
//
// A Supervisor inverts that. Components declare what they depend on, the
// supervisor runs each one whose dependencies are satisfied, and a failure
// retries with backoff instead of ending the process. Components that need
// consensus depend on the one component that waits for it, so their failure to
// converge holds back exactly themselves.
package boot

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Supervisor runs components until they converge, and keeps them converged.
type Supervisor struct {
	opts   Options
	logger *zap.Logger

	mu       sync.Mutex
	order    []*state
	byName   map[string]*state
	onChange []func(Snapshot)
	addErr   error
}

// New creates a supervisor. A nil logger is replaced with a no-op.
func New(logger *zap.Logger, opts Options) *Supervisor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Supervisor{
		opts:   opts.withDefaults(),
		logger: logger,
		byName: make(map[string]*state),
	}
}

// Add registers a component. Every name in DependsOn must already have been
// added: requiring registration order to be a valid dependency order makes
// cycles impossible to express and lets one pass converge the whole graph.
//
// Errors are recorded rather than returned so a graph can be declared as a
// list of calls; Err and Run both surface the first one.
func (s *Supervisor) Add(c Component) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validate(c); err != nil {
		if s.addErr == nil {
			s.addErr = err
		}
		return
	}

	st := &state{Component: c, status: StatusPending, backoff: s.opts.BaseBackoff}
	s.order = append(s.order, st)
	s.byName[c.Name] = st
}

func (s *Supervisor) validate(c Component) error {
	if c.Name == "" {
		return fmt.Errorf("boot: component with no name")
	}
	if _, dup := s.byName[c.Name]; dup {
		return fmt.Errorf("boot: component %q registered twice", c.Name)
	}
	if c.Reconcile == nil {
		return fmt.Errorf("boot: component %q has no Reconcile", c.Name)
	}
	for _, dep := range c.DependsOn {
		if _, ok := s.byName[dep]; !ok {
			return fmt.Errorf("boot: component %q depends on %q, which is not registered before it", c.Name, dep)
		}
	}
	return nil
}

// Err returns the first registration error, if any.
func (s *Supervisor) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addErr
}

// OnChange registers a callback invoked whenever a component's status changes.
// Callbacks run on the supervisor goroutine with no lock held, so they must not
// block for long and must not call back into Add.
func (s *Supervisor) OnChange(fn func(Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = append(s.onChange, fn)
}

// Snapshot returns the current status of every component, in dependency order.
func (s *Supervisor) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Supervisor) snapshotLocked() Snapshot {
	out := Snapshot{Components: make([]ComponentStatus, 0, len(s.order))}
	for _, st := range s.order {
		out.Components = append(out.Components, ComponentStatus{
			Name:       st.Name,
			Status:     st.status,
			Attempts:   st.attempts,
			LastErr:    st.lastErr,
			ReadySince: st.readySince,
		})
	}
	return out
}

// Run converges the components and keeps converging them until ctx is done.
// It returns the registration error if the graph is invalid, and ctx.Err()
// otherwise. It never returns because a component failed.
func (s *Supervisor) Run(ctx context.Context) error {
	if err := s.Err(); err != nil {
		return err
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		earliest := s.pass(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}

		wait := idleInterval
		if !earliest.IsZero() {
			wait = time.Until(earliest)
		}
		if wait < 0 {
			wait = 0
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// pass walks the components once in dependency order and returns the earliest
// time any of them wants attention again. Status changes are published as they
// happen rather than at the end of the pass, so a caller watching for
// "degraded" sees it as soon as the local tier is up — not after the cluster
// gate has finished its attempt.
func (s *Supervisor) pass(ctx context.Context) time.Time {
	var earliest time.Time

	consider := func(t time.Time) {
		if t.IsZero() {
			return
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}

	for _, st := range s.components() {
		if ctx.Err() != nil {
			return earliest
		}

		if !s.depsReady(st) {
			if s.block(st) {
				s.notify()
			}
			continue
		}

		now := time.Now()
		if st.status == StatusReady {
			if st.Health == nil {
				continue
			}
			if now.Before(st.nextHealth) {
				consider(st.nextHealth)
				continue
			}
			if err := st.Health(ctx); err != nil {
				s.logger.Warn("Boot component failed its health check, reconciling again",
					zap.String("component", st.Name), zap.Error(err))
				s.regress(st, err)
				s.notify()
				consider(time.Now())
				continue
			}
			s.setNextHealth(st, now.Add(s.opts.HealthInterval))
			consider(st.nextHealth)
			continue
		}

		if now.Before(st.nextAttempt) {
			consider(st.nextAttempt)
			continue
		}

		err := st.Reconcile(ctx)
		if err != nil {
			next := s.fail(st, err)
			s.logger.Warn("Boot component not converged yet, will retry",
				zap.String("component", st.Name),
				zap.Int("attempt", st.attempts),
				zap.Duration("retry_in", time.Until(next)),
				zap.Error(err))
			s.notify()
			consider(next)
			continue
		}

		s.succeed(st)
		s.logger.Info("Boot component ready",
			zap.String("component", st.Name),
			zap.Int("attempts", st.attempts))
		s.notify()
		consider(st.nextHealth)
	}

	return earliest
}
