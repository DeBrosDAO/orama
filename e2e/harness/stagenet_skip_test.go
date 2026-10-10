package harness

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// withTarget runs fn in a subtest whose Fleet is a state of the given target,
// and reports whether the subtest was skipped, failed, and with what message.
func withTarget(t *testing.T, target string, fn func(t *testing.T)) (skipped, failed bool) {
	t.Helper()
	saved := current.fleet
	current.fleet = &fleet.Fleet{State: &fleet.State{Target: target, RunID: "ab12"}}
	defer func() { current.fleet = saved }()
	t.Run("inner", func(t *testing.T) {
		defer func() { skipped, failed = t.Skipped(), t.Failed() }()
		fn(t)
	})
	return skipped, failed
}

func TestRequireNotStagenet_skipsOnStagenetOnly(t *testing.T) {
	skipped, failed := withTarget(t, config.TargetStagenet, func(t *testing.T) { requireNotStagenet(t, "ExtraNode") })
	if !skipped || failed {
		t.Fatalf("stagenet: skipped=%v failed=%v, want a skip", skipped, failed)
	}
	skipped, failed = withTarget(t, config.TargetFleet, func(t *testing.T) { requireNotStagenet(t, "ExtraNode") })
	if skipped || failed {
		t.Fatalf("fleet: skipped=%v failed=%v, want to run on", skipped, failed)
	}
}

func TestRequireArchive(t *testing.T) {
	cases := []struct {
		name, target, archive string
		wantSkip              bool
	}{
		{"stagenet without an archive skips", config.TargetStagenet, "", true},
		{"stagenet with an archive runs", config.TargetStagenet, "/a.tar.gz", false},
		{"fleet without an archive is left to fail", config.TargetFleet, "", false},
		{"fleet with an archive runs", config.TargetFleet, "/a.tar.gz", false},
	}
	for _, c := range cases {
		skipped, failed := withTarget(t, c.target, func(t *testing.T) { RequireArchive(t, c.archive) })
		if skipped != c.wantSkip || failed {
			t.Errorf("%s: skipped=%v failed=%v", c.name, skipped, failed)
		}
	}
}
