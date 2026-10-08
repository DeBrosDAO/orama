package globalnode

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/install"
)

const (
	relayUnit    = "orama-global-tor-relay.service"
	dirauthUnit  = "orama-global-tor-dirauth.service"
	archiveTimer = "orama-global-tor-archive.timer"
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
	if !slices.Equal(f.calls, []string{"start " + relayUnit}) {
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

func TestLifecycle_directoryAuthorityRunsItsArchiveTimer(t *testing.T) {
	l, f := newLifecycle(t, install.GlobalServiceDirauth)
	if err := l.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"start " + dirauthUnit, "start " + archiveTimer}; !slices.Equal(f.calls, want) {
		t.Fatalf("start calls = %v, want %v", f.calls, want)
	}
	f.calls = nil
	if err := l.Stop(nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"stop " + archiveTimer, "stop " + dirauthUnit}; !slices.Equal(f.calls, want) {
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
