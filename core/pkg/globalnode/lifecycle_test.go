package globalnode

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/install"
)

// fakeSystemd answers systemctl from a map of unit states and records every
// call, in order, with the RPC wait and the floor check among them.
type fakeSystemd struct {
	states  map[string]string
	calls   []string
	failOn  string
	floor   error
	rpcDown bool
}

func (f *fakeSystemd) systemctl(args ...string) ([]byte, error) {
	call := strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if call == f.failOn {
		return []byte("Job failed"), errors.New("exit status 1")
	}
	unit := args[len(args)-1]
	switch args[0] {
	case "is-active":
		state := f.states[unit]
		if state == "" {
			state = "inactive"
		}
		if state != "active" {
			return []byte(state + "\n"), errors.New("exit status 3")
		}
		return []byte("active\n"), nil
	case "start", "restart":
		f.states[unit] = "active"
	case "stop":
		f.states[unit] = "inactive"
	}
	return nil, nil
}

func newLifecycle(t *testing.T, installed ...install.GlobalService) (Lifecycle, *fakeSystemd) {
	t.Helper()
	dir := t.TempDir()
	for _, s := range installed {
		if err := os.WriteFile(filepath.Join(dir, install.GlobalServiceUnit(s)), []byte("[Unit]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeSystemd{states: map[string]string{}}
	return Lifecycle{
		Systemctl: f.systemctl,
		UnitDir:   dir,
		WaitChainRPC: func(context.Context) error {
			f.calls = append(f.calls, "wait-rpc")
			if f.rpcDown {
				return errors.New("connection refused")
			}
			return nil
		},
		CheckSignFloor: func() error {
			f.calls = append(f.calls, "check-floor")
			return f.floor
		},
		Out: io.Discard,
	}, f
}

const (
	chainUnit    = "orama-global-chain.service"
	providerUnit = "orama-global-provider.service"
	archiverUnit = "orama-global-archiver.service"
)

func TestLifecycleStart_chainFirstThenRPCThenServices(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceChain, install.GlobalServiceProvider, install.GlobalServiceArchiver)
	if err := l.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"check-floor", "enable " + chainUnit, "start " + chainUnit, "wait-rpc", "start " + providerUnit, "start " + archiverUnit}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
}

func TestLifecycleStart_serviceAloneNeedsARunningChain(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceChain, install.GlobalServiceProvider)
	err := l.Start(context.Background(), []install.GlobalService{install.GlobalServiceProvider})
	if err == nil || !strings.Contains(err.Error(), "orama global start chain") {
		t.Fatalf("err = %v, want a refusal naming the chain", err)
	}
	if slices.Contains(f.calls, "start "+providerUnit) {
		t.Fatal("the provider was started without its chain")
	}
	f.states[chainUnit] = "active"
	if err := l.Start(context.Background(), []install.GlobalService{install.GlobalServiceProvider}); err != nil {
		t.Fatalf("with the chain running: %v", err)
	}
}

func TestLifecycleStart_rpcDownLeavesServicesStopped(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceChain, install.GlobalServiceProvider)
	f.rpcDown = true
	if err := l.Start(context.Background(), nil); err == nil {
		t.Fatal("start succeeded with the chain RPC down")
	}
	if slices.Contains(f.calls, "start "+providerUnit) {
		t.Fatal("the provider was started before the chain RPC answered")
	}
}

func TestLifecycleStart_signFloorRefusalStartsNothing(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceChain)
	f.floor = errors.New("behind")
	err := l.Start(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to start the chain") {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(f.calls, "start "+chainUnit) {
		t.Fatal("the chain was started below its sign floor")
	}
}

func TestLifecycleStop_chainStopsDependentsFirst(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceChain, install.GlobalServiceProvider, install.GlobalServiceArchiver)
	if err := l.Stop([]install.GlobalService{install.GlobalServiceChain}); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop " + archiverUnit, "stop " + providerUnit, "stop " + chainUnit}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
}

func TestLifecycleStop_failureIsReturned(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceChain, install.GlobalServiceProvider)
	f.failOn = "stop " + providerUnit
	err := l.Stop(nil)
	if err == nil || !strings.Contains(err.Error(), providerUnit) {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(f.calls, "stop "+chainUnit) {
		t.Fatal("the chain was stopped after a dependent failed to stop")
	}
}

func TestLifecycleRestart_chainRestartsEverythingInOrder(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceChain, install.GlobalServiceProvider)
	if err := l.Restart(context.Background(), []install.GlobalService{install.GlobalServiceChain}); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop " + providerUnit, "stop " + chainUnit, "check-floor", "enable " + chainUnit, "start " + chainUnit, "wait-rpc", "start " + providerUnit}
	if !slices.Equal(f.calls, want) {
		t.Fatalf("calls = %v, want %v", f.calls, want)
	}
}

func TestLifecycle_notInstalledIsAnError(t *testing.T) {
	l, _ := newLifecycle(t, install.GlobalServiceChain)
	if err := l.Stop([]install.GlobalService{install.GlobalServiceRepair}); err == nil {
		t.Fatal("stopping an uninstalled repair unit succeeded")
	}
	empty, _ := newLifecycle(t)
	if _, err := empty.Status(); err == nil {
		t.Fatal("status with no unit installed succeeded")
	}
}

func TestLifecycleStatus_readsEachState(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceChain, install.GlobalServiceProvider)
	f.states[chainUnit] = "active"
	f.states[providerUnit] = "failed"
	states, err := l.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 || states[0].Active != "active" || states[1].Active != "failed" {
		t.Fatalf("states = %+v", states)
	}
	if err := l.ChainStopped(); err == nil {
		t.Fatal("ChainStopped accepted an active chain")
	}
	f.states[chainUnit] = "failed"
	if err := l.ChainStopped(); err != nil {
		t.Fatalf("a failed chain is not running: %v", err)
	}
}

func TestLifecycleStatus_emptySystemctlOutputIsAnError(t *testing.T) {
	l, _ := newLifecycle(t, install.GlobalServiceChain)
	l.Systemctl = func(...string) ([]byte, error) { return nil, errors.New("no bus") }
	if _, err := l.Status(); err == nil {
		t.Fatal("status with no systemctl answer succeeded")
	}
}
