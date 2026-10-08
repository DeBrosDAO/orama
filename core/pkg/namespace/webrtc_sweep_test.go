package namespace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

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

// An SFU that is allocated, env file and all, is left alone: nothing is
// retired, and the unit is not even looked at.
func TestRetireIfUnallocated_allocatedUnitIsLeftAlone(t *testing.T) {
	needsCalled, retired := false, false
	got, err := retireIfUnallocated(
		func() bool { return false },
		func() (bool, error) { needsCalled = true; return true, nil },
		func() error { retired = true; return nil })
	if err != nil || got || retired || needsCalled {
		t.Fatalf("allocated: retired=%v err=%v retire called=%v needs called=%v", got, err, retired, needsCalled)
	}
}

// The allocation read under the lock decides, not the sweep's first read: a
// unit whose allocation came back between the two is left alone.
func TestRetireIfUnallocated_decidesOnTheReadUnderTheLock(t *testing.T) {
	reads := 0
	gone := func() bool { reads++; return reads == 1 } // gone on the sweep's read, back on the locked one
	if !gone() {
		t.Fatal("setup: the first read must say gone")
	}
	retired := false
	got, err := retireIfUnallocated(gone,
		func() (bool, error) { return true, nil },
		func() error { retired = true; return nil })
	if err != nil || got || retired {
		t.Fatalf("an allocation that came back was retired: %v %v %v", got, err, retired)
	}
}

func TestRetireIfUnallocated(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name       string
		needs      func() (bool, error)
		retire     error
		want       bool
		wantErr    bool
		wantRetire bool
	}{
		{"unallocated, still provisioned", func() (bool, error) { return true, nil }, nil, true, false, true},
		{"unallocated, already retired", func() (bool, error) { return false, nil }, nil, false, false, false},
		{"unit unreadable", func() (bool, error) { return false, boom }, nil, false, true, false},
		{"retire fails", func() (bool, error) { return true, nil }, boom, false, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called := false
			got, err := retireIfUnallocated(func() bool { return true }, c.needs,
				func() error { called = true; return c.retire })
			if got != c.want || (err != nil) != c.wantErr || called != c.wantRetire {
				t.Errorf("got %v, %v (retire called %v); want %v, err=%v, retire=%v", got, err, called, c.want, c.wantErr, c.wantRetire)
			}
		})
	}
}

// SpawnSFU takes the namespace's lock, so the sweep's retireSFU (which removes
// the env file and config under it) cannot land between the spawn's writes of
// them and its start.
func TestSpawnSFU_waitsForTheNamespaceLock(t *testing.T) {
	nsBase := t.TempDir()
	s := NewSystemdSpawner(nsBase, "", zap.NewNop())
	configs := filepath.Join(nsBase, "acme", "configs")

	unlock := mustLockNamespace(t, s, "acme")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.SpawnSFU(context.Background(), "acme", "node-1", SFUInstanceConfig{})
	}()
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(configs); err == nil {
		t.Fatal("SpawnSFU started writing while the namespace lock was held")
	}
	unlock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("SpawnSFU did not proceed after the lock was released")
	}
	if _, err := os.Stat(configs); err != nil {
		t.Fatalf("SpawnSFU never ran after the lock was released: %v", err)
	}
}
