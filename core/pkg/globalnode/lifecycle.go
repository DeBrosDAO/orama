package globalnode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/globalnetns"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
)

// unitDir is where the global installer writes units.
const unitDir = "/etc/systemd/system"

// Lifecycle starts, stops and reports the installed orama-global-* units.
// The chain starts first and stops last: every other service reaches it only
// through its loopback RPC.
type Lifecycle struct {
	// Systemctl runs systemctl as root and returns its combined output.
	Systemctl func(args ...string) ([]byte, error)
	// UnitDir is where the installed units are.
	UnitDir string
	// WaitChainRPC returns once the chain's RPC answers, or ctx is done.
	WaitChainRPC func(ctx context.Context) error
	// CheckSignFloor is the double-sign guard run before the chain starts.
	CheckSignFloor func() error
	Out            io.Writer
}

// DefaultLifecycle is this node: systemctl through the privileged helper's
// path (root runs it directly), the chain's loopback RPC, and the sign floor
// in the global state root.
//
// On a co-located machine (the netns unit is installed) the chain's RPC is on
// the orama-global namespace's loopback, and the wait probes it from inside.
func DefaultLifecycle(out io.Writer) Lifecycle {
	return defaultLifecycle(unitDir, out)
}

func defaultLifecycle(dir string, out io.Writer) Lifecycle {
	wait := WaitChainRPC
	if _, err := os.Stat(filepath.Join(dir, globalnetns.UnitName)); err == nil {
		wait = WaitChainRPCInNamespace
	}
	return Lifecycle{
		Systemctl: func(args ...string) ([]byte, error) {
			return privhelper.Command(privhelper.ToolSystemctl, args...).CombinedOutput()
		},
		UnitDir:        dir,
		WaitChainRPC:   wait,
		CheckSignFloor: DefaultHost().CheckSignFloor,
		Out:            out,
	}
}

// Installed is the installed services, in start order.
func (l Lifecycle) Installed() ([]install.GlobalService, error) {
	var out []install.GlobalService
	for _, s := range install.GlobalServiceOrder {
		path := filepath.Join(l.UnitDir, install.GlobalServiceUnit(s))
		_, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("check %s: %w", path, err)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no orama-global unit is installed in %s; run the global install first", l.UnitDir)
	}
	return out, nil
}

// targets is the installed services named by only, or all of them.
func (l Lifecycle) targets(only []install.GlobalService) (installed, targets []install.GlobalService, err error) {
	installed, err = l.Installed()
	if err != nil {
		return nil, nil, err
	}
	if len(only) == 0 {
		return installed, installed, nil
	}
	for _, s := range installed {
		if slices.Contains(only, s) {
			targets = append(targets, s)
		}
	}
	for _, s := range only {
		if !slices.Contains(installed, s) {
			return nil, nil, fmt.Errorf("%s is not installed on this node", s)
		}
	}
	return installed, targets, nil
}

// Start starts the named services (all installed ones when none is named).
// The chain goes first, after the sign-floor check, and the others wait for
// its RPC. A service started without the chain needs the chain running.
func (l Lifecycle) Start(ctx context.Context, only []install.GlobalService) error {
	_, targets, err := l.targets(only)
	if err != nil {
		return err
	}
	if slices.Contains(targets, install.GlobalServiceChain) {
		if err := l.startChain(ctx); err != nil {
			return err
		}
	} else if err := l.requireChainActive(); err != nil {
		return err
	}
	for _, s := range targets {
		if s == install.GlobalServiceChain {
			continue
		}
		if err := l.unit("start", s); err != nil {
			return err
		}
	}
	return nil
}

func (l Lifecycle) startChain(ctx context.Context) error {
	if err := l.CheckSignFloor(); err != nil {
		return fmt.Errorf("refusing to start the chain: %w", err)
	}
	// enable, since a migration export disables the chain unit.
	for _, verb := range []string{"enable", "start"} {
		if err := l.unit(verb, install.GlobalServiceChain); err != nil {
			return err
		}
	}
	fmt.Fprintf(l.Out, "  waiting for the chain RPC...\n")
	if err := l.WaitChainRPC(ctx); err != nil {
		return fmt.Errorf("the chain started but its RPC did not answer, so the services that need it were not started: %w", err)
	}
	return nil
}

// Stop stops the named services in reverse order. Stopping the chain stops
// every installed service that needs it first.
func (l Lifecycle) Stop(only []install.GlobalService) error {
	installed, targets, err := l.targets(only)
	if err != nil {
		return err
	}
	if slices.Contains(targets, install.GlobalServiceChain) {
		targets = installed
	}
	for i := len(targets) - 1; i >= 0; i-- {
		if err := l.unit("stop", targets[i]); err != nil {
			return err
		}
	}
	return nil
}

// Restart stops then starts the named services. Restarting the chain
// restarts every installed service, in order.
func (l Lifecycle) Restart(ctx context.Context, only []install.GlobalService) error {
	installed, targets, err := l.targets(only)
	if err != nil {
		return err
	}
	if slices.Contains(targets, install.GlobalServiceChain) {
		targets = installed
	}
	if err := l.Stop(targets); err != nil {
		return err
	}
	return l.Start(ctx, targets)
}

// State is the systemd ActiveState of one installed service.
type State struct {
	Service install.GlobalService
	Unit    string
	Active  string
}

// Status is the state of every installed service.
func (l Lifecycle) Status() ([]State, error) {
	installed, err := l.Installed()
	if err != nil {
		return nil, err
	}
	out := make([]State, 0, len(installed))
	for _, s := range installed {
		active, err := l.activeState(s)
		if err != nil {
			return nil, err
		}
		out = append(out, State{Service: s, Unit: install.GlobalServiceUnit(s), Active: active})
	}
	return out, nil
}

// DisableChain disables the chain unit so it does not start at boot. A
// migration export runs it after the chain is stopped; the chain unit's own
// ExecStartPre sign-floor check refuses any start that still happens.
func (l Lifecycle) DisableChain() error {
	return l.unit("disable", install.GlobalServiceChain)
}

// ChainStopped reports an error unless the chain unit is not running.
func (l Lifecycle) ChainStopped() error {
	state, err := l.activeState(install.GlobalServiceChain)
	if err != nil {
		return err
	}
	if state == "inactive" || state == "failed" {
		return nil
	}
	return fmt.Errorf("the chain is %s; stop it first with: orama global stop chain", state)
}

func (l Lifecycle) requireChainActive() error {
	state, err := l.activeState(install.GlobalServiceChain)
	if err != nil {
		return err
	}
	if state != "active" {
		return fmt.Errorf("the chain is %s and the other services reach it only on its RPC; start it first with: orama global start chain", state)
	}
	return nil
}

// activeState is `systemctl is-active`. It exits non-zero for every state but
// active, so the printed state is what counts; no state printed is an error.
func (l Lifecycle) activeState(s install.GlobalService) (string, error) {
	unit := install.GlobalServiceUnit(s)
	out, err := l.Systemctl("is-active", unit)
	state := strings.TrimSpace(string(out))
	if state == "" || strings.ContainsAny(state, " \n") {
		return "", fmt.Errorf("systemctl is-active %s: %v: %q", unit, err, state)
	}
	return state, nil
}

func (l Lifecycle) unit(verb string, s install.GlobalService) error {
	unit := install.GlobalServiceUnit(s)
	if out, err := l.Systemctl(verb, unit); err != nil {
		return fmt.Errorf("systemctl %s %s: %w\n%s", verb, unit, err, strings.TrimSpace(string(out)))
	}
	fmt.Fprintf(l.Out, "  %s: %s\n", verb, unit)
	return nil
}
