package autoupdate

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/nodehealth"
)

// Plan is one node's upgrade to a release Decide said to install.
type Plan struct {
	Settings Settings
	// Verify checks the release before anything is touched: the TUF check of
	// the archive (releaseverify.CheckFile) and the extracted files the
	// swaps read. Required; a failure aborts with nothing changed.
	Verify func() error
	// Files are the binaries to replace, each with a file Verify covered.
	// Verify and the stage step read each Next by path, one after the
	// other, so every Next must sit in a directory only root can write (the
	// 0700 staging directory stage-archive extracts into); otherwise what
	// is copied need not be what was verified.
	Files []FileSwap
	// Stop and Start are the node's own service lifecycle (the orama CLI's
	// ordered stop and start), never raw unit commands.
	Stop  func() error
	Start func() error
	// Health is where the gate reads this node, through pkg/nodehealth, the
	// check `orama node start` and `orama node upgrade` wait on.
	Health        nodehealth.Target
	HealthOptions nodehealth.Options
}

// Upgrade installs the release in p on this node: verify, stop, stage and
// swap every file, start, then the health gate. A failure after anything
// changed undoes what ran, last first — the start is stopped, each file is
// renamed back, the stop is started — and the gate is read again so the
// caller knows whether the node came back on the previous release.
//
// releaseBad tells the caller to mark the release bad. It is set only when
// the new binaries ran and failed: the start or the health gate. A failed
// stop or a failed copy or rename is an error about this node, not about
// the release, and leaves releaseBad false.
//
// Only auto runs, and never for a validator: Settings.validate refuses
// auto for RoleValidator.
func Upgrade(ctx context.Context, p Plan) (releaseBad bool, err error) {
	if err := p.check(); err != nil {
		return false, err
	}
	if err := p.Verify(); err != nil {
		return false, fmt.Errorf("the release did not verify, nothing was changed: %w", err)
	}
	gate := func() error { return nodehealth.WaitReady(ctx, p.Health, p.HealthOptions) }
	releaseBad, err = Apply(p.steps(), gate)
	if err == nil {
		return false, nil
	}
	if healthErr := gate(); healthErr != nil {
		return releaseBad, errors.Join(err, fmt.Errorf("the node is not healthy on the previous release either: %w", healthErr))
	}
	return releaseBad, err
}

// check refuses a plan that could not be run safely.
func (p Plan) check() error {
	if err := p.Settings.validate(); err != nil {
		return err
	}
	if p.Settings.Mode != ModeAuto {
		return fmt.Errorf("auto-update mode is %s; only auto installs a release", p.Settings.Mode)
	}
	if p.Verify == nil || p.Stop == nil || p.Start == nil {
		return fmt.Errorf("an upgrade needs a verification, a stop and a start")
	}
	if len(p.Files) == 0 {
		return fmt.Errorf("an upgrade needs at least one file to swap")
	}
	return nil
}

// steps are stop, each file's stage and swap, and start.
func (p Plan) steps() []Step {
	steps := []Step{{Name: "stop", Do: p.Stop, Undo: p.Start}}
	for _, f := range p.Files {
		steps = append(steps, f.Steps()...)
	}
	return append(steps, Step{Name: "start", Do: p.startOrStop, Undo: p.Stop, Blames: true})
}

// startOrStop starts the node and, when the start fails, stops it again: a
// half-started node would keep running new binaries through the rollback.
func (p Plan) startOrStop() error {
	if err := p.Start(); err != nil {
		return errors.Join(err, p.Stop())
	}
	return nil
}
