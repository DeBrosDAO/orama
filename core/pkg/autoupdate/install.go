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
	// is the staged one from the moment Stage returns.
	Current() string
	// Stage verifies the downloaded release against the release root and puts
	// it in place under /opt/orama, keeping the release it replaces. The node
	// is unchanged if it fails.
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

// Install puts rel on the node and gates on its health (Stage, then Finish).
func Install(ctx context.Context, n Node, rel Release) (Result, error) {
	if err := n.Stage(ctx, rel); err != nil {
		return Result{Unchanged: true}, fmt.Errorf("stage release %s, the node is unchanged: %w", rel.Version, err)
	}
	return Finish(ctx, n, rel.Version)
}

// Finish upgrades the node onto the release staged under /opt/orama and gates
// on its health. A failure puts the previous release back and upgrades onto it.
// The node is then on the release it had, and the error says whether it is
// healthy there. It is also what a run that finds a staged release its
// predecessor did not finish calls.
//
// A cancelled context is not a failure of the release: the run was stopped,
// nothing is rolled back, and the next run finishes the install.
func Finish(ctx context.Context, n Node, version string) (Result, error) {
	err := n.Upgrade(ctx)
	if err == nil {
		err = n.Healthy(ctx)
	}
	if err == nil {
		return Result{Installed: true}, nil
	}
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("stopped while installing release %s; the next run finishes it: %w", version, errors.Join(err, ctx.Err()))
	}
	notStarted := errors.Is(err, ErrNotStarted)
	res := Result{ReleaseBad: !notStarted}
	if restoreErr := n.Restore(ctx); restoreErr != nil {
		return res, errors.Join(err, fmt.Errorf("put the previous release back: %w", restoreErr))
	}
	if !notStarted {
		if upErr := n.Upgrade(ctx); upErr != nil {
			return res, errors.Join(err, fmt.Errorf("upgrade onto the previous release %s: %w", n.Current(), upErr))
		}
	}
	if healthErr := n.Healthy(ctx); healthErr != nil {
		return res, errors.Join(err, fmt.Errorf("the node is not healthy on the previous release either: %w", healthErr))
	}
	res.RolledBack = true
	return res, fmt.Errorf("release %s did not install and the node is back on its previous release: %w", version, err)
}
