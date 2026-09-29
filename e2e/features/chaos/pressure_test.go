//go:build e2e_fleet

package chaos

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/services"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// diskTarget is above the 85% warning (core/pkg/telemetry/cluster/alerts_node.go).
	diskTarget  = 90
	diskAlert   = "Disk at"
	alertBudget = 4 * time.Minute
	// memShare is how much of the node's available memory the hog takes;
	// memHeadroom is its cgroup limit above that.
	memShare    = 60
	memHeadroom = 64
	memHeld     = 80 // percent of the hog's size that must be resident
)

// alertFor reports whether the monitor lists an alert of subsystem for n
// containing text.
func alertFor(t testing.TB, n fleet.Node, subsystem, text string) bool {
	t.Helper()
	for _, a := range services.Alerts(t, subsystem) {
		if strings.Contains(a, text) && (strings.Contains(a, n.PublicIP) || (n.WGIP != "" && strings.Contains(a, n.WGIP))) {
			return true
		}
	}
	return false
}

// TestChaos_diskPressureAlertsAndDegradesGracefully: the filesystem a node
// reports is pushed to 90% (always leaving a GiB free). The monitor warns
// about that node's disk, the node keeps serving and accepting writes, and
// once the space is freed the warning clears and the cluster converges
// (docs/MONITORING.md: disk > 85% is a warning).
func TestChaos_diskPressureAlertsAndDegradesGracefully(t *testing.T) {
	f := harness.Fleet(t)
	victim := infra.Followers(t, infra.RequireHealthy(t))[0]
	s := newStore(t)
	t.Run("filled", func(t *testing.T) {
		before, fill := realistic.FillDisk(t, f, victim, diskTarget)
		t.Logf("%s: disk was %d%% of %d MiB; filled %d MiB", victim.Name, before.Pct, before.TotalMB, fill)
		eventually.Require(t, pollEvery, alertBudget, "the disk warning for "+victim.Name, func() (bool, error) {
			return alertFor(t, victim, "system", diskAlert), nil
		})
		requireServing(t, []fleet.Node{victim}, "with its disk at 90%")
		s.writeN(t, victim, "disk", rowsPerPhase)
	})
	eventually.Require(t, pollEvery, alertBudget, "the disk warning for "+victim.Name+" to clear", func() (bool, error) {
		return !alertFor(t, victim, "system", diskAlert), nil
	})
	healed(t, "the cluster after the disk pressure")
	s.requireRows(t, victim, "disk", rowsPerPhase)
}

// TestChaos_memoryPressureKeepsDaemonsAlive: a cgroup-limited process holds
// most of a node's available memory. It is resident (the pressure is real),
// the node keeps serving and writing, no node daemon is OOM-killed (an OOM
// kill is a critical alert, which a converged cluster has none of), and after
// it stops the cluster is converged.
func TestChaos_memoryPressureKeepsDaemonsAlive(t *testing.T) {
	f := harness.Fleet(t)
	victim := infra.Followers(t, infra.RequireHealthy(t))[0]
	s := newStore(t)
	t.Run("pressured", func(t *testing.T) {
		mb := availableMB(t, f, victim) * memShare / 100
		unit := realistic.MemoryHog(t, f, victim, mb, mb+memHeadroom)
		eventually.Require(t, pollEvery, recoverBudget, "the hog to hold its memory", func() (bool, error) {
			held := realistic.MemoryCurrentMB(t, f, victim, unit)
			return held >= mb*memHeld/100, fmt.Errorf("%d of %d MiB resident", held, mb)
		})
		requireServing(t, []fleet.Node{victim}, "under memory pressure")
		s.writeN(t, victim, "memory", rowsPerPhase)
		healed(t, "the cluster under memory pressure (no OOM kill, no crash loop)")
	})
	healed(t, "the cluster after the memory pressure")
}

// availableMB is the node's MemAvailable in MiB.
func availableMB(t testing.TB, f *fleet.Fleet, n fleet.Node) int {
	t.Helper()
	out := f.MustExec(t, n, "awk '/^MemAvailable:/ {print int($2/1024)}' /proc/meminfo").Stdout
	v, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || v <= 0 {
		t.Fatalf("%s: MemAvailable %q: %v", n.Name, out, err)
	}
	return v
}
