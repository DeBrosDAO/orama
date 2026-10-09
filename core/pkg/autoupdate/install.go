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
	// Restore puts the previous release, which is version, back in place under
	// /opt/orama. It refuses a kept release that is another one.
	Restore(ctx context.Context, version string) error
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
	// StageErr is what Stage reported although the release was in place (a
	// step after the swap, such as removing its staging directory, failed). The
	// install went on and the node is healthy, so it is not a failure; the
	// caller says so.
	StageErr error
}

// Settled is a result that leaves the node in a state no later run needs to
// finish or repair: installed, rolled back, or never touched.
func (r Result) Settled() bool { return r.Installed || r.RolledBack || r.Unchanged }

// Install puts the release in on the node and gates on its health (Stage, then
// Finish). in is the intent the caller has already journaled.
//
// A stage that fails is the node unchanged only if /opt/orama still holds the
// previous release: a step after the swap can fail with the swap done, and the
// install then goes on, for the health gate to judge, with the stage's error
// kept (joined into a failure, or in Result.StageErr). A node whose installed
// release is neither one is not settled, and its intent stays.
func Install(ctx context.Context, n Node, j Journal, in Intent, rel Release) (Result, error) {
	stageErr := n.Stage(ctx, rel)
	if stageErr != nil {
		if proceed, res, err := judgeFailedStage(n, in, stageErr); !proceed {
			return res, err
		}
	}
	res, err := Finish(ctx, n, j, in)
	if stageErr == nil {
		return res, err
	}
	stageErr = fmt.Errorf("stage release %s reported a failure with the release in place: %w", in.Version, stageErr)
	if err != nil {
		return res, errors.Join(err, stageErr)
	}
	res.StageErr = stageErr
	return res, nil
}

// judgeFailedStage says whether the install goes on after stageErr, and if not
// how it ended: unchanged when the node still holds the release it had, and
// unsettled when what it holds cannot be told or is neither release.
func judgeFailedStage(n Node, in Intent, stageErr error) (proceed bool, res Result, err error) {
	switch cur := n.Current(); {
	case cur == in.Version:
		return true, Result{}, nil
	case cur != "" && cur == in.Previous:
		return false, Result{Unchanged: true}, fmt.Errorf("stage release %s, the node is unchanged: %w", in.Version, stageErr)
	default:
		return false, Result{}, fmt.Errorf("stage release %s failed and the node's installed release is %q, "+
			"neither the release it had (%s) nor the new one; the install stays recorded so that the next run recovers the node: %w",
			in.Version, cur, in.Previous, stageErr)
	}
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
		err = errors.Join(err, fmt.Errorf("record that the rollback has begun (the rollback goes on but is not crash-safe: "+
			"a run killed before it ends cannot tell it from a stage that changed nothing, so it will not finish it or mark the release bad): %w", jerr))
	}
	return RollBack(ctx, n, in, err)
}

// RollBack puts the previous release back and brings the node up on it: what
// Finish does after a failure, and what the next run does for a rollback a
// killed run had begun (cause is then nil). The restore is skipped if the
// previous release is already in place, and the upgrade onto it if the failure
// changed nothing. After a restore the node must hold the previous release: a
// restore that left another (a kept release older than the previous one, a
// half-restored tree) is an error, and the node is not reported rolled back.
func RollBack(ctx context.Context, n Node, in Intent, cause error) (Result, error) {
	res := Result{ReleaseBad: in.Blame}
	if n.Current() != in.Previous {
		if err := restoreTo(ctx, n, in); err != nil {
			return res, errors.Join(cause, err)
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

// restoreTo puts the previous release back and checks that it is what the node
// holds.
func restoreTo(ctx context.Context, n Node, in Intent) error {
	if err := n.Restore(ctx, in.Previous); err != nil {
		return fmt.Errorf("put the previous release back: %w", err)
	}
	if got := n.Current(); got != in.Previous {
		return fmt.Errorf("the restore left the node on release %q, not on the previous release %s; "+
			"the node needs a person to put the right release back", got, in.Previous)
	}
	return nil
}
