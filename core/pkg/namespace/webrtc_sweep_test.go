package namespace

import (
	"errors"
	"testing"
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
