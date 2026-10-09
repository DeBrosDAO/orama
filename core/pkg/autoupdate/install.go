package autoupdate

import (
	"context"
	"errors"
	"fmt"
)

// ErrNotStarted marks an upgrade that failed before it changed anything on the
// node: a check it makes first refused. The release is not to blame for that,
// and the services never stopped.
var ErrNotStarted = errors.New("the upgrade changed nothing on the node")

// Node is the machine the agent runs on, as the install sees it.
type Node interface {
	// Current is the release this node runs: the one under /opt/orama, which
	// is the staged one from the moment Stage swaps it in. "" when the
	// installed manifest cannot be read.
	Current() string
	// Recover undoes a swap of /opt/orama that a killed run left half-done, so
	// that Current can be read.
	Recover(ctx context.Context) error
	// Stage verifies the downloaded release against the release root and puts
	// it in place under /opt/orama, keeping the release it replaces. The node
	// is unchanged if it fails before the swap.
	Stage(ctx context.Context, rel Release) error
	// Upgrade upgrades the node onto what is staged, restarting its services
	// (`orama node upgrade --restart`). An error that wraps ErrNotStarted
	// changed nothing.
	Upgrade(ctx context.Context) error
	// Restore puts the previous release back in place under /opt/orama.
	Restore(ctx context.Context) error
	// Healthy waits for the node to carry its share of the cluster again.
	Healthy(ctx context.Context) error
}

// Result is how an install ended.
type Result struct {
	// Installed: the node runs the release and is healthy.
	Installed bool
	// RolledBack: the node is back on its previous release and healthy.
	RolledBack bool
	// Unchanged: the release was never put in place.
	Unchanged bool
	// ReleaseBad: the release ran on this node and failed, so no other node
	// should install it.
	ReleaseBad bool
}

// Settled is a result that leaves the node in a state no later run needs to
// finish or repair: installed, rolled back, or never touched.
func (r Result) Settled() bool { return r.Installed || r.RolledBack || r.Unchanged }

// Install puts the release in on the node and gates on its health (Stage, then
// Finish). in is the intent the caller has already journaled.
//
// A stage that fails is the node unchanged only if /opt/orama still holds the
// previous release: a step after the swap can fail with the swap done, and the
// install then goes on, for the health gate to judge.
func Install(ctx context.Context, n Node, j Journal, in Intent, rel Release) (Result, error) {
	if err := n.Stage(ctx, rel); err != nil && n.Current() != in.Version {
		return Result{Unchanged: true}, fmt.Errorf("stage release %s, the node is unchanged: %w", in.Version, err)
	}
	return Finish(ctx, n, j, in)
}

// Finish upgrades the node onto the release staged under /opt/orama and gates
// on its health. A failure puts the previous release back and upgrades onto it;
// the intent says the rollback has begun before it does anything, so a run
// killed in the middle of it is finished by the next. The node is then on the
// release it had, and the error says whether it is healthy there. It is also
// what a run that finds a staged release its predecessor did not finish calls.
//
// A cancelled context is not a failure of the release: the run was stopped,
// nothing is rolled back, and the next run finishes the install.
func Finish(ctx context.Context, n Node, j Journal, in Intent) (Result, error) {
	err := n.Upgrade(ctx)
	if err == nil {
		err = n.Healthy(ctx)
	}
	if err == nil {
		return Result{Installed: true}, nil
	}
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("stopped while installing release %s; the next run finishes it: %w", in.Version, errors.Join(err, ctx.Err()))
	}
	in.RollingBack, in.Blame = true, !errors.Is(err, ErrNotStarted)
	if jerr := j.Replace(in); jerr != nil {
		err = errors.Join(err, fmt.Errorf("record that the rollback has begun: %w", jerr))
	}
	return RollBack(ctx, n, in, err)
}

// RollBack puts the previous release back and brings the node up on it: what
// Finish does after a failure, and what the next run does for a rollback a
// killed run had begun (cause is then nil). The restore is skipped if the
// previous release is already in place, and the upgrade onto it if the failure
// changed nothing.
func RollBack(ctx context.Context, n Node, in Intent, cause error) (Result, error) {
	res := Result{ReleaseBad: in.Blame}
	if n.Current() == in.Version {
		if err := n.Restore(ctx); err != nil {
			return res, errors.Join(cause, fmt.Errorf("put the previous release back: %w", err))
		}
	}
	if in.Blame {
		if err := n.Upgrade(ctx); err != nil {
			return res, errors.Join(cause, fmt.Errorf("upgrade onto the previous release %s: %w", in.Previous, err))
		}
	}
	if err := n.Healthy(ctx); err != nil {
		return res, errors.Join(cause, fmt.Errorf("the node is not healthy on the previous release either: %w", err))
	}
	res.RolledBack = true
	if cause == nil {
		cause = errors.New("a previous run began rolling it back and this one finished")
	}
	return res, fmt.Errorf("release %s did not install and the node is back on its previous release: %w", in.Version, cause)
}
