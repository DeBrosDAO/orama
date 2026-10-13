package node

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func recordingTick(edge *error) (*heartbeatTick, *[]string) {
	var calls []string
	return &heartbeatTick{
		heartbeat:   func(context.Context) { calls = append(calls, "heartbeat") },
		edgeServing: func() error { return *edge },
		advertise:   func(context.Context) { calls = append(calls, "advertise") },
		withdraw:    func(context.Context, error) { calls = append(calls, "withdraw") },
		maintain:    func(context.Context) { calls = append(calls, "maintain") },
	}, &calls
}

func TestHeartbeatTick_servingEdgeAdvertisesAfterTheHeartbeat(t *testing.T) {
	var edge error
	tick, calls := recordingTick(&edge)
	tick.run(context.Background())
	if want := []string{"heartbeat", "advertise", "maintain"}; !slices.Equal(*calls, want) {
		t.Fatalf("calls = %v, want %v: the node re-adds itself after re-asserting active and before the purge", *calls, want)
	}
}

// A stopped Caddy is not a dead node: the heartbeat that namespace recovery and
// the overlay fan-outs read as liveness goes on; only the A records go.
func TestHeartbeatTick_edgeDownWithdrawsButKeepsHeartbeating(t *testing.T) {
	edge := errors.New("not active: orama-namespace-caddy@index.service")
	tick, calls := recordingTick(&edge)
	for range edgeDownTicks {
		tick.run(context.Background())
	}
	want := []string{"heartbeat", "maintain", "heartbeat", "withdraw", "maintain"}
	if !slices.Equal(*calls, want) {
		t.Fatalf("calls = %v, want %v: withdrawn on the second down tick, heartbeating throughout", *calls, want)
	}
}

// A Caddy restart reads as inactive for one check; the node must not leave
// the round-robin for it.
func TestHeartbeatTick_oneDownTickIsARestartNotAnOutage(t *testing.T) {
	edge := errors.New("activating")
	tick, calls := recordingTick(&edge)
	tick.run(context.Background())
	edge = nil
	tick.run(context.Background())
	edge = errors.New("activating")
	tick.run(context.Background())
	if slices.Contains(*calls, "withdraw") {
		t.Fatalf("calls = %v: single down ticks between serving ones withdrew the node", *calls)
	}
}

func TestHeartbeatTick_edgeBackAdvertisesAgain(t *testing.T) {
	edge := errors.New("down")
	tick, calls := recordingTick(&edge)
	for range edgeDownTicks {
		tick.run(context.Background())
	}
	edge = nil
	tick.run(context.Background())
	if got := (*calls)[len(*calls)-2]; got != "advertise" {
		t.Fatalf("calls = %v: the first serving tick after an outage must advertise", *calls)
	}
}
