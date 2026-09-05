package boot

import (
	"context"
	"time"
)

// Status is where a component is in its convergence.
type Status string

const (
	// StatusBlocked means at least one dependency is not ready. The component
	// is not attempted and is not counted as failing.
	StatusBlocked Status = "blocked"
	// StatusPending means the component is eligible to run and has not
	// converged: either it has never been attempted, or its last attempt
	// failed and it is waiting out its backoff.
	StatusPending Status = "pending"
	// StatusReady means the last Reconcile succeeded and no Health check has
	// contradicted it since.
	StatusReady Status = "ready"
)

// Component is one unit of node start-up work.
type Component struct {
	// Name identifies the component in logs and snapshots. Required, unique.
	Name string

	// DependsOn names components that must be StatusReady before this one is
	// attempted. Every name must already be registered — see Supervisor.Add.
	DependsOn []string

	// Reconcile brings the component to its desired state.
	//
	// The contract is three rules:
	//
	//   - It must be idempotent. The supervisor calls it again after every
	//     failure, and again whenever a Health check or a dependency
	//     regresses. Anything it starts once (a goroutine, a subscription)
	//     must be guarded so a second call does not start it twice.
	//
	//   - It must return within a bounded time. One pass runs components
	//     sequentially, so a Reconcile that blocks forever stalls every other
	//     component. Wrap it in WithTimeout unless it already bounds itself.
	//
	//   - The context it receives is the supervisor's run context, cancelled
	//     only at shutdown. Goroutines started from Reconcile may hold it and
	//     will live as long as the node does. WithTimeout narrows it for the
	//     attempt without affecting that.
	Reconcile func(context.Context) error

	// Health, when non-nil, is polled while the component is ready. A failure
	// returns it to StatusPending and schedules another Reconcile, which also
	// blocks its dependents until it converges again. Leave it nil for work
	// that cannot regress, such as writing a file.
	Health func(context.Context) error
}

// WithTimeout bounds a single Reconcile or Health attempt. The wrapped
// function still sees a context derived from the run context, so cancelling
// the supervisor cancels the attempt.
func WithTimeout(d time.Duration, fn func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		attempt, cancel := context.WithTimeout(ctx, d)
		defer cancel()
		return fn(attempt)
	}
}

// Backoff and polling defaults. A zero field in Options takes the matching
// default.
const (
	// DefaultBaseBackoff is the delay before the second attempt.
	DefaultBaseBackoff = 1 * time.Second
	// DefaultMaxBackoff caps the delay between attempts, so a component that
	// is waiting on something external still notices within a minute of it
	// becoming available.
	DefaultMaxBackoff = 60 * time.Second
	// DefaultHealthInterval is how often a ready component's Health runs.
	DefaultHealthInterval = 30 * time.Second
)

// idleInterval is how long Run sleeps when nothing is scheduled. Reached only
// when every component is ready and none declares a Health check.
const idleInterval = time.Minute

// Options tunes the converge loop.
type Options struct {
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	HealthInterval time.Duration
}

func (o Options) withDefaults() Options {
	if o.BaseBackoff <= 0 {
		o.BaseBackoff = DefaultBaseBackoff
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = DefaultMaxBackoff
	}
	if o.MaxBackoff < o.BaseBackoff {
		o.MaxBackoff = o.BaseBackoff
	}
	if o.HealthInterval <= 0 {
		o.HealthInterval = DefaultHealthInterval
	}
	return o
}

// ComponentStatus is a point-in-time view of one component.
type ComponentStatus struct {
	Name     string
	Status   Status
	Attempts int
	// LastErr is the error from the most recent failed attempt. It is not
	// cleared when the component becomes ready, so an operator can still see
	// what a component struggled with on the way up.
	LastErr    error
	ReadySince time.Time
}

// Snapshot is a point-in-time view of every component, in dependency order.
type Snapshot struct {
	Components []ComponentStatus
}

// AllReady reports whether every component has converged.
func (s Snapshot) AllReady() bool {
	for _, c := range s.Components {
		if c.Status != StatusReady {
			return false
		}
	}
	return true
}

// NotReady lists the names of components that have not converged, in
// dependency order.
func (s Snapshot) NotReady() []string {
	var names []string
	for _, c := range s.Components {
		if c.Status != StatusReady {
			names = append(names, c.Name)
		}
	}
	return names
}
