package namespace

import (
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/systemd"
)

// TestWebRTCSweepFor: an unreadable config touches nothing; a namespace with
// no WebRTC config has its orphan units stopped (an SFU left running there held
// ports the allocator gave the next namespace, which crash-looped on "address
// already in use"); an enabled namespace is reconciled.
func TestWebRTCSweepFor(t *testing.T) {
	cases := []struct {
		name string
		cfg  *WebRTCConfig
		err  error
		want webrtcSweep
	}{
		{"read failed", nil, errors.New("leader election"), webrtcSweepSkip},
		{"read failed with a config", &WebRTCConfig{}, errors.New("partial"), webrtcSweepSkip},
		{"not enabled", nil, nil, webrtcSweepStopOrphans},
		{"enabled", &WebRTCConfig{}, nil, webrtcSweepReconcile},
	}
	for _, c := range cases {
		if got := webrtcSweepFor(c.cfg, c.err); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestHoldsOrMayRetakePorts: a crash-looping orphan SFU (activating, or failed
// between restarts) was never stopped because only "active" counted.
func TestHoldsOrMayRetakePorts(t *testing.T) {
	for state, want := range map[systemd.ActiveState]bool{
		systemd.ActiveStateActive:       true,
		systemd.ActiveStateActivating:   true,
		systemd.ActiveStateFailed:       true,
		systemd.ActiveStateDeactivating: true,
		systemd.ActiveStateInactive:     false,
		"":                              false,
	} {
		if got := holdsOrMayRetakePorts(state); got != want {
			t.Errorf("%q: %v, want %v", state, got, want)
		}
	}
}

// TestNeedsRetiring: an unallocated SFU that is stopped and disabled but still
// has its env file is still provisioned: `orama node status` listed it as an
// inactive service ("32 of 33 running") and the next upgrade would enable and
// start it again. Inactive alone, with no env file, is retired.
func TestNeedsRetiring(t *testing.T) {
	cases := []struct {
		state  systemd.ActiveState
		hasEnv bool
		want   bool
	}{
		{systemd.ActiveStateInactive, true, true},
		{systemd.ActiveStateInactive, false, false},
		{"", false, false},
		{systemd.ActiveStateActive, false, true},
		{systemd.ActiveStateFailed, true, true},
	}
	for _, c := range cases {
		if got := needsRetiring(c.state, c.hasEnv); got != c.want {
			t.Errorf("state %q env %v: %v, want %v", c.state, c.hasEnv, got, c.want)
		}
	}
}
