package report

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// procRoot is where the kernel's process filesystem is; tests point it at a
// fixture.
var procRoot = "/proc"

// stealSampleWindow is how long CPU counters are sampled to measure steal.
// Steal is a rate, so it needs two readings; half a second is long enough to
// see a hypervisor taking a fifth of the CPU and short enough not to slow the
// report.
var stealSampleWindow = 500 * time.Millisecond

// pressureUnknown marks a pressure figure the kernel does not expose.
const pressureUnknown = -1

// bpfFrameworkFeature is the systemd build feature SocketBindAllow and
// SocketBindDeny depend on.
const bpfFrameworkFeature = "+BPF_FRAMEWORK"

// systemdFeatures is `systemctl --version`; tests replace it.
var systemdFeatures = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), localCommandTimeout)
	defer cancel()
	return runCmd(ctx, "systemctl", "--version")
}

// socketBindEnforced reports whether this systemd enforces socket-bind rules.
func socketBindEnforced() bool {
	out, err := systemdFeatures()
	return err == nil && strings.Contains(out, bpfFrameworkFeature)
}

// collectCPUContention fills the steal and pressure figures.
func collectCPUContention(r *SystemReport) {
	r.CPUStealPct = sampleSteal()
	r.PressureCPUPct = readPressure("cpu")
	r.PressureIOPct = readPressure("io")
	r.PressureMemPct = readPressure("memory")
}

// sampleSteal is the steal share of all CPU time between two readings of
// /proc/stat, or 0 when they cannot be read.
func sampleSteal() float64 {
	before, ok := readCPUTimes()
	if !ok {
		return 0
	}
	time.Sleep(stealSampleWindow)
	after, ok := readCPUTimes()
	if !ok {
		return 0
	}
	// Counters only go backwards when CPUs are hot-unplugged; a sample
	// across that says nothing.
	if after.total <= before.total || after.steal < before.steal {
		return 0
	}
	total := after.total - before.total
	return float64(after.steal-before.steal) * 100 / float64(total)
}

type cpuTimes struct{ total, steal uint64 }

// readCPUTimes reads the aggregate "cpu" line of /proc/stat: user nice system
// idle iowait irq softirq steal guest guest_nice. guest time is already
// counted in user, so it is not added to the total.
func readCPUTimes() (cpuTimes, bool) {
	data, err := os.ReadFile(filepath.Join(procRoot, "stat"))
	if err != nil {
		return cpuTimes{}, false
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(line)
	const stealField = 8
	if len(fields) <= stealField || fields[0] != "cpu" {
		return cpuTimes{}, false
	}
	var t cpuTimes
	for i, f := range fields[1 : stealField+1] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return cpuTimes{}, false
		}
		t.total += v
		if i+1 == stealField {
			t.steal = v
		}
	}
	return t, true
}

// readPressure is the "some avg60" figure of /proc/pressure/<resource>, or
// pressureUnknown when the kernel has no PSI.
func readPressure(resource string) float64 {
	data, err := os.ReadFile(filepath.Join(procRoot, "pressure", resource))
	if err != nil {
		return pressureUnknown
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "some ") {
			continue
		}
		for _, kv := range strings.Fields(line)[1:] {
			if v, ok := strings.CutPrefix(kv, "avg60="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					return f
				}
			}
		}
	}
	return pressureUnknown
}
