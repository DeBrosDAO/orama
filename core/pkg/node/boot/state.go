package boot

import (
	"time"
)

// state is a component plus its convergence bookkeeping.
type state struct {
	Component

	status     Status
	attempts   int
	lastErr    error
	readySince time.Time

	nextAttempt time.Time
	backoff     time.Duration
	nextHealth  time.Time
}

// components returns the registration-ordered slice under the lock. The slice
// itself is never mutated after Run starts, so iterating it outside the lock
// is safe; the per-component fields are read and written only on the
// supervisor goroutine, except through Snapshot, which takes the lock.
func (s *Supervisor) components() []*state {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.order
}

func (s *Supervisor) depsReady(st *state) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, dep := range st.DependsOn {
		if s.byName[dep].status != StatusReady {
			return false
		}
	}
	return true
}

// block moves a component to StatusBlocked and reports whether that changed
// anything. A component that becomes blocked after being ready has its backoff
// reset, so it retries immediately once its dependency returns.
func (s *Supervisor) block(st *state) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.status == StatusBlocked {
		return false
	}
	st.status = StatusBlocked
	st.readySince = time.Time{}
	st.backoff = s.opts.BaseBackoff
	st.nextAttempt = time.Time{}
	return true
}

func (s *Supervisor) regress(st *state, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.status = StatusPending
	st.lastErr = err
	st.readySince = time.Time{}
	st.backoff = s.opts.BaseBackoff
	st.nextAttempt = time.Now()
}

func (s *Supervisor) fail(st *state, err error) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.status = StatusPending
	st.attempts++
	st.lastErr = err
	st.readySince = time.Time{}
	st.nextAttempt = time.Now().Add(st.backoff)
	st.backoff *= 2
	if st.backoff > s.opts.MaxBackoff {
		st.backoff = s.opts.MaxBackoff
	}
	return st.nextAttempt
}

func (s *Supervisor) succeed(st *state) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	st.status = StatusReady
	st.attempts++
	st.readySince = now
	st.backoff = s.opts.BaseBackoff
	st.nextAttempt = time.Time{}
	if st.Health != nil {
		st.nextHealth = now.Add(s.opts.HealthInterval)
	}
}

func (s *Supervisor) setNextHealth(st *state, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.nextHealth = at
}

func (s *Supervisor) notify() {
	s.mu.Lock()
	snap := s.snapshotLocked()
	callbacks := make([]func(Snapshot), len(s.onChange))
	copy(callbacks, s.onChange)
	s.mu.Unlock()

	for _, fn := range callbacks {
		fn(snap)
	}
}
