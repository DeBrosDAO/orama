package globalnode

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/install"
)

const (
	relayUnit    = "orama-global-tor-relay.service"
	dirauthUnit  = "orama-global-tor-dirauth.service"
	archiveTimer = "orama-global-tor-archive.timer"
	monitorTimer = "orama-global-tor-monitor.timer"
	onionUnit    = "orama-global-tor-onion.service"
	gateUnit     = "orama-global-txgate.service"
)

// A Tor relay does not use the chain: it starts with no chain installed or
// running, and a chain stop or restart leaves it serving.
func TestLifecycle_torRelayIsStandalone(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceRelay)
	if err := l.Start(context.Background(), nil); err != nil {
		t.Fatalf("a relay host with no chain: %v", err)
	}
	if !slices.Equal(f.calls, []string{"start " + relayUnit, "start " + monitorTimer}) {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestLifecycle_chainStopAndRestartLeaveTheRelayRunning(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceChain, install.GlobalServiceProvider, install.GlobalServiceRelay)
	f.states[chainUnit], f.states[providerUnit], f.states[relayUnit] = "active", "active", "active"
	if err := l.Stop([]install.GlobalService{install.GlobalServiceChain}); err != nil {
		t.Fatal(err)
	}
	if f.states[relayUnit] != "active" || f.states[chainUnit] != "inactive" || f.states[providerUnit] != "inactive" {
		t.Fatalf("states after a chain stop: %v", f.states)
	}
	f.calls = nil
	if err := l.Restart(context.Background(), []install.GlobalService{install.GlobalServiceChain}); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if strings.Contains(c, relayUnit) {
			t.Fatalf("a chain restart touched the relay: %v", f.calls)
		}
	}
}

func TestLifecycle_directoryAuthorityRunsItsArchiveAndMonitorTimers(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceDirauth)
	if err := l.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"start " + dirauthUnit, "start " + archiveTimer, "start " + monitorTimer}; !slices.Equal(f.calls, want) {
		t.Fatalf("start calls = %v, want %v", f.calls, want)
	}
	f.calls = nil
	if err := l.Stop(nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"stop " + archiveTimer, "stop " + monitorTimer, "stop " + dirauthUnit}; !slices.Equal(f.calls, want) {
		t.Fatalf("stop calls = %v, want %v", f.calls, want)
	}
}

func TestLifecycle_onionServiceNeedsTheChainAndStartsItsGate(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceChain, install.GlobalServiceOnion)
	err := l.Start(context.Background(), []install.GlobalService{install.GlobalServiceOnion})
	if err == nil || !strings.Contains(err.Error(), "orama global start chain") {
		t.Fatalf("err = %v, want a refusal naming the chain", err)
	}
	f.states[chainUnit] = "active"
	if err := l.Start(context.Background(), []install.GlobalService{install.GlobalServiceOnion}); err != nil {
		t.Fatal(err)
	}
	if f.states[onionUnit] != "active" || f.states[gateUnit] != "active" {
		t.Fatalf("states = %v", f.states)
	}
}

func TestLifecycle_aDirectoryAuthorityIsNotStoppedWhileAnotherIsLearning(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceDirauth)
	f.states[dirauthUnit] = "active"
	l.CheckAuthorityRoll = func() error { return errors.New("OramaAuth2 started 5m ago") }
	for name, act := range map[string]func() error{
		"stop": func() error { return l.Stop(nil) },
		"restart": func() error {
			return l.Restart(context.Background(), []install.GlobalService{install.GlobalServiceDirauth})
		},
	} {
		f.calls = nil
		err := act()
		if err == nil || !strings.Contains(err.Error(), "OramaAuth2 started 5m ago") || !strings.Contains(err.Error(), "--force") {
			t.Errorf("%s: err = %v, want the reason and the way to override it", name, err)
		}
		if len(f.calls) != 0 || f.states[dirauthUnit] != "active" {
			t.Errorf("%s touched the authority: calls %v, state %q", name, f.calls, f.states[dirauthUnit])
		}
	}
}

func TestLifecycle_forceStopsADirectoryAuthorityAnyway(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceDirauth)
	f.states[dirauthUnit] = "active"
	l.CheckAuthorityRoll = func() error { return errors.New("OramaAuth2 started 5m ago") }
	l.ForceAuthorityRoll = true
	if err := l.Stop(nil); err != nil {
		t.Fatal(err)
	}
	if f.states[dirauthUnit] != "inactive" {
		t.Fatalf("state = %q", f.states[dirauthUnit])
	}
}

func TestLifecycle_theAuthorityCheckOnlyGuardsDirectoryAuthorities(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceRelay)
	f.states[relayUnit] = "active"
	l.CheckAuthorityRoll = func() error { return errors.New("must not run for a relay") }
	if err := l.Restart(context.Background(), nil); err != nil {
		t.Fatalf("a relay restart: %v", err)
	}
}
