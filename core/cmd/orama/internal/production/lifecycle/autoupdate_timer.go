package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/utils"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/nodehealth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/systemd"
)

// interruptedProbeBudget bounds the one look at whether the node serves that an interrupted
// restart takes before it decides whether to start the timer: the operator asked to stop, so it
// does not wait for the node as a restart that is left to finish does.
const interruptedProbeBudget = 10 * time.Second

// timerUnits are the systemd operations a restart makes on the auto-update timer
// and the agent run it fires.
type timerUnits struct {
	// active reports whether the timer is installed and running.
	active func(unit string) bool
	stop   func(unit string) error
	start  func(unit string) error
	// stopRun stops the agent service (a run in progress, if any). A host without the unit has
	// nothing to stop and it returns nil.
	stopRun func(unit string) error
}

// systemdTimerUnits acts on this machine's systemd.
var systemdTimerUnits = timerUnits{
	active: func(unit string) bool {
		if !utils.ServiceUnitExists(unit) {
			return false
		}
		running, _ := utils.IsServiceActive(unit)
		return running
	},
	stop:  func(unit string) error { return exec.Command("systemctl", "stop", unit).Run() },
	start: func(unit string) error { return exec.Command("systemctl", "start", unit).Run() },
	stopRun: func(unit string) error {
		if !utils.ServiceUnitExists(unit) {
			return nil
		}
		return exec.Command("systemctl", "stop", unit).Run()
	},
}

// restartWithAutoUpdatePaused runs restart with the auto-update timer stopped, and starts the
// timer again only when the node then serves and the timer was running before.
//
// The agent reads the cluster's settings from this node's own rqlite. A timer that falls due
// while the restart has the node down runs the agent against a half-stopped node, which fails
// ("connection refused") and leaves a failed unit in `orama status` until the next run, up to 15
// minutes later. So the timer is stopped first, and so is a run of the agent already in progress
// (orama-autoupdate.service), which would be using the node as the restart takes it down.
//
// The timer's OnBootSec deadline is long past on a running host, so starting it makes it fire at
// once (within its randomized delay). It is therefore started only after the node is known to
// answer. When the restart fails, or the node does not come to serve, the timer is left stopped
// and the output names the command that starts it once the node serves: starting it for the
// sake of "automatic updates are never left off" would fire the agent at a node that is down. A
// reboot starts it too, since it is only stopped, not disabled.
//
// ctx is cancelled by an interrupt, a terminate or a hangup (an ssh session that dropped). A
// restart cannot be abandoned half way without leaving services stopped, so it is finished; then
// the node is looked at once, and the timer is started if the node serves and otherwise left
// stopped with the same message. A second interrupt is the operator's, and ends the process.
func restartWithAutoUpdatePaused(ctx context.Context, units timerUnits, restart func() error, serving func(context.Context) error) error {
	const unit = systemd.AutoUpdateTimerName
	timerWasOn := units.active(unit)
	if timerWasOn {
		if err := units.stop(unit); err != nil {
			return clierr.Failure("could not stop %s before the restart, so it could run the update agent against a node that is down: %v", unit, err)
		}
		fmt.Printf("  Stopped %s for the restart\n", unit)
	}
	if err := units.stopRun(systemd.AutoUpdateServiceName); err != nil {
		return errors.Join(restoreTimer(units, timerWasOn), clierr.Failure("could not stop %s, a run of the update agent that would use the node while it restarts: %v",
			systemd.AutoUpdateServiceName, err))
	}

	stopWatching := announceInterrupt(ctx)
	restartErr := restart()
	nodeErr := restartErr
	if restartErr == nil && timerWasOn {
		nodeErr = serving(ctx)
	}
	stopWatching()
	interrupted := ctx.Err() != nil
	if interrupted {
		nodeErr = interruptedState(ctx, restartErr, serving)
	}
	var timerErr error
	if timerWasOn {
		timerErr = resumeTimer(units, nodeErr)
	}
	if interrupted {
		nodeErr = errors.Join(clierr.Failure("interrupted"), nodeErr)
	}
	return errors.Join(nodeErr, timerErr)
}

// resumeTimer starts the timer again if the node serves (nodeErr is nil). Otherwise it leaves the
// timer stopped and says how to start it.
func resumeTimer(units timerUnits, nodeErr error) error {
	const unit = systemd.AutoUpdateTimerName
	if nodeErr != nil {
		fmt.Printf("  %s stays stopped: starting it now would run the update agent at once against a node that is not serving.\n"+
			"  When the node serves again, start it with: systemctl start %s (a reboot starts it too)\n", unit, unit)
		return nil
	}
	if err := units.start(unit); err != nil {
		return clierr.Failure("could not start %s again: %v; start it with: systemctl start %s", unit, err, unit)
	}
	fmt.Printf("  Started %s\n", unit)
	return nil
}

// restoreTimer starts the timer again when the restart never began and the timer was running.
func restoreTimer(units timerUnits, timerWasOn bool) error {
	if !timerWasOn {
		return nil
	}
	if err := units.start(systemd.AutoUpdateTimerName); err != nil {
		return clierr.Failure("could not start %s again: %v; start it with: systemctl start %s",
			systemd.AutoUpdateTimerName, err, systemd.AutoUpdateTimerName)
	}
	return nil
}

// interruptedState is what an interrupted restart reports about the node: why the restart failed
// if it did, otherwise the result of one look at whether the node serves now (nil when it does).
// The wait the interrupt cut short says nothing about the node.
func interruptedState(ctx context.Context, restartErr error, serving func(context.Context) error) error {
	if restartErr != nil {
		return restartErr
	}
	probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), interruptedProbeBudget)
	defer cancel()
	return serving(probe)
}

// announceInterrupt tells the operator, as soon as ctx is cancelled, that the restart is being
// finished and not dropped. The returned function ends the watch, and says it first if ctx was
// cancelled meanwhile without the watch having been scheduled yet; it is said once.
func announceInterrupt(ctx context.Context) (stop func()) {
	var once sync.Once
	say := func() {
		once.Do(func() {
			fmt.Printf("\n  Interrupted: finishing the restart so no service is left stopped (interrupt again to abort)\n")
		})
	}
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-ctx.Done():
			say()
		case <-done:
		}
	}()
	return func() {
		close(done)
		<-exited
		if ctx.Err() != nil {
			say()
		}
	}
}

// waitNodeServing waits until this node answers: its index rqlite has a leader
// and its gateway serves. did is what was done to the services, for the message.
// Cancelling ctx ends the wait.
func waitNodeServing(ctx context.Context, did string) error {
	indexRQLite, err := rqlite.LocalNodeEndpoint()
	if err != nil {
		return clierr.Failure("Services were %s but the index RQLite cannot be addressed: %v", did, err)
	}
	if err := nodehealth.WaitReady(ctx, nodehealth.Target{
		RQLite:      indexRQLite,
		GatewayBase: fmt.Sprintf("http://localhost:%d", constants.GatewayAPIPort),
	}, nodehealth.Options{Budget: startReadyBudget}); err != nil {
		return clierr.Failure("Services were %s but the node is not serving: %v", did, err)
	}
	return nil
}
