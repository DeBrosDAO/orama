//go:build e2e_fleet

package realistic

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const (
	// fillDir is where disk pressure is put: a temporary directory on the
	// filesystem that holds /opt/orama, which is what the node's disk report
	// measures (core/pkg/telemetry/report/system.go reads / and /opt/orama).
	fillDir = "/var/tmp"
	// minFreeMB is the free space a fill always leaves, whatever the target:
	// the node must keep writing its journal, rqlite log and caches.
	minFreeMB = 1024
	mbShift   = 20
)

// DiskUse is a filesystem's size, used space and use percent.
type DiskUse struct {
	TotalMB, UsedMB, AvailMB, Pct int
}

// diskUse reads df for path on n, in MiB.
func diskUse(t testing.TB, f *fleet.Fleet, n fleet.Node, path string) DiskUse {
	t.Helper()
	out := f.MustExec(t, n, "df -B1M --output=size,used,avail,pcent "+path+" | tail -n 1").Stdout
	fields := strings.Fields(strings.ReplaceAll(out, "%", ""))
	if len(fields) != 4 {
		t.Fatalf("%s: df %s printed %q", n.Name, path, out)
	}
	var v [4]int
	for i, s := range fields {
		x, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("%s: df %s field %q: %v", n.Name, path, s, err)
		}
		v[i] = x
	}
	return DiskUse{TotalMB: v[0], UsedMB: v[1], AvailMB: v[2], Pct: v[3]}
}

// FillDisk raises the use of the filesystem holding /opt/orama on n to
// targetPct with one preallocated file under /var/tmp, never leaving less
// than minFreeMB free, and only when /var/tmp is that same filesystem (a fill
// elsewhere would pressure nothing the node measures). The file is removed at
// cleanup and the removal checked. It returns the use before and the fill.
func FillDisk(t testing.TB, f *fleet.Fleet, n fleet.Node, targetPct int) (DiskUse, int) {
	t.Helper()
	sameFS := f.MustExec(t, n, "test \"$(stat -c %d "+fillDir+")\" = \"$(stat -c %d /opt/orama)\" && echo same || echo other").Stdout
	if strings.TrimSpace(sameFS) != "same" {
		t.Fatalf("%s: %s is not on the filesystem of /opt/orama; filling it would pressure nothing the node measures", n.Name, fillDir)
	}
	before := diskUse(t, f, n, "/opt/orama")
	want := before.TotalMB*targetPct/100 - before.UsedMB
	fillMB := min(want, before.AvailMB-minFreeMB)
	if fillMB <= 0 {
		t.Fatalf("%s: the disk is at %d%% with %d MiB free; filling it to %d%% would leave less than %d MiB", n.Name, before.Pct, before.AvailMB, targetPct, minFreeMB)
	}
	path := fmt.Sprintf("%s/e2e-diskfill-%s-%s", fillDir, f.State.RunID, newTag(t))
	t.Cleanup(func() { edge.RunInCleanup(t, f, n, "rm -f "+path+" && ! test -e "+path) })
	f.MustExec(t, n, fmt.Sprintf("fallocate -l %dM %s", fillMB, path))
	return before, fillMB
}

// memHogPrefix names the transient units MemoryHog starts.
const memHogPrefix = "e2e-memhog-"

// The hog's cgroup bounds, in percent of what it holds. MemoryHigh throttles
// it (reclaim pressure, never a kill) a little above its size; MemoryMax is
// only a backstop well above that. Hitting MemoryMax would be a cgroup OOM
// kill, which writes "Memory cgroup out of memory" to the kernel log and
// raises the node's permanent critical OOM alert
// (core/pkg/telemetry/report/system.go counts `dmesg | grep -ci 'out of
// memory'`): a pressure test must never cause one (OOMKills checks it).
const (
	hogHighPct = 110
	hogMaxPct  = 150
	percent    = 100
)

// MemoryHog starts a transient unit on n that allocates and holds mb MiB,
// throttled above hogHighPct and capped at hogMaxPct of that (no swap), so
// the pressure is real and neither the hog nor a node daemon is killed. The
// caller keeps mb*hogMaxPct/100 below the node's available memory. The
// cleanup stops it and checks it is gone.
func MemoryHog(t testing.TB, f *fleet.Fleet, n fleet.Node, mb int) string {
	t.Helper()
	unit := memHogPrefix + f.State.RunID + "-" + newTag(t)
	t.Cleanup(func() {
		edge.RunInCleanup(t, f, n, "systemctl stop "+unit+" 2>/dev/null; systemctl reset-failed "+unit+" 2>/dev/null; ! systemctl is-active --quiet "+unit)
	})
	// tail keeps its whole input (no newline) in memory, then blocks writing
	// to a reader that never reads: the memory stays held until the stop.
	hog := fmt.Sprintf("head -c %dM /dev/zero | tail | sleep infinity", mb)
	f.MustExec(t, n, fmt.Sprintf("systemd-run --unit=%s -p MemoryHigh=%dM -p MemoryMax=%dM -p MemorySwapMax=0 --collect sh -c %s",
		unit, mb*hogHighPct/percent, mb*hogMaxPct/percent, fleet.ShellQuote(hog)))
	return unit
}

// HogFootprintPct is the most of its size a MemoryHog may use (its MemoryMax),
// in percent: a caller sizes the hog so this much still fits.
const HogFootprintPct = hogMaxPct

// oomKillWindow mirrors report.OOMKillWindowArg in core/pkg/telemetry/report
// (this module does not link core's telemetry packages).
const oomKillWindow = "1h"

// OOMKills is the node's count of kernel OOM kills in the telemetry window
// (oomKillWindow), read from the same
// journal query: a pressure test records it before and asserts it unchanged
// after, since a kill inside the window is a critical alert on that node.
func OOMKills(t testing.TB, f *fleet.Fleet, n fleet.Node) int {
	t.Helper()
	cmd := fmt.Sprintf("sudo -n journalctl -k --no-pager -o cat --since %s | grep -c 'Killed process' || true",
		fleet.ShellQuote("-"+oomKillWindow))
	out := strings.TrimSpace(f.MustExec(t, n, cmd).Stdout)
	v, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("%s: the kernel log OOM count is %q: %v", n.Name, out, err)
	}
	return v
}

// MemoryCurrentMB is the unit's cgroup memory.current in MiB.
func MemoryCurrentMB(t testing.TB, f *fleet.Fleet, n fleet.Node, unit string) int {
	t.Helper()
	out := strings.TrimSpace(f.MustExec(t, n, "systemctl show -p MemoryCurrent --value "+unit).Stdout)
	v, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		t.Fatalf("%s: %s has no memory reading (MemoryCurrent=%q): memory accounting is off", n.Name, unit, out)
	}
	return int(v >> mbShift)
}

// FDCount is how many file descriptors the unit's main process holds.
func FDCount(t testing.TB, f *fleet.Fleet, n fleet.Node, unit string) int {
	t.Helper()
	out := f.MustExec(t, n, "pid=$(systemctl show -p MainPID --value "+unit+"); test \"$pid\" -gt 0 && ls /proc/$pid/fd | wc -l || echo 0").Stdout
	v, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("%s: fd count of %s: %q", n.Name, unit, out)
	}
	return v
}
