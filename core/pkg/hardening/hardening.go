// Package hardening is the single source of the kernel settings an Orama node
// is hardened with (secret-bearing memory stays off the block device) and the
// check that reads them back. Install writes and verifies them; every node
// report reads them again at runtime, because a package postinst, apport, a
// stray `sysctl -w` or a re-enabled swap changes them silently afterwards.
package hardening

import (
	"fmt"
	"strconv"
	"strings"
)

// Sysctl is one hardened kernel setting: its key, its /proc/sys file and the
// value install sets.
type Sysctl struct {
	Key  string
	Path string
	Want string
}

// DiscardCorePattern pipes every core dump to /bin/false, which discards it:
// the same outcome as systemd-coredump's Storage=none, without depending on
// which crash handler a distribution installs.
const DiscardCorePattern = "|/bin/false"

// Sysctls is what the install drop-in sets and the runtime check expects.
var Sysctls = []Sysctl{
	{Key: "fs.suid_dumpable", Path: "/proc/sys/fs/suid_dumpable", Want: "0"},
	{Key: "kernel.core_pattern", Path: "/proc/sys/kernel/core_pattern", Want: DiscardCorePattern},
	{Key: "kernel.yama.ptrace_scope", Path: "/proc/sys/kernel/yama/ptrace_scope", Want: "1"},
}

const (
	// ApportUnit is Ubuntu's crash reporter. Its start writes
	// fs.suid_dumpable=2 and its own core_pattern, after systemd-sysctl has
	// applied the drop-in. Debian does not ship it.
	ApportUnit = "apport.service"
	// SwapsPath lists the active swap areas, one per line after a header.
	SwapsPath = "/proc/swaps"

	apportNotFound = "not-found"
	apportMasked   = "masked"
)

// DropIn renders the sysctl.d file that sets Sysctls.
func DropIn() string {
	var b strings.Builder
	b.WriteString("# Orama: keep secret-bearing pages off the block device and out of other processes\n")
	for _, s := range Sysctls {
		fmt.Fprintf(&b, "%s = %s\n", s.Key, s.Want)
	}
	return b.String()
}

// ApportDisabled reports whether an apport LoadState means apport cannot run:
// the unit is not installed or is masked.
func ApportDisabled(loadState string) bool {
	return loadState == apportNotFound || loadState == apportMasked
}

// Live is what a node's kernel and systemd say right now about each hardened
// setting.
type Live struct {
	// Sysctls maps a Sysctl key to its live value; a key that could not be
	// read is absent and named in Errors.
	Sysctls map[string]string `json:"sysctls"`
	// SwapDevices counts the active swap areas.
	SwapDevices int `json:"swap_devices"`
	// ApportLoadState is systemd's LoadState of ApportUnit.
	ApportLoadState string `json:"apport_load_state"`
	// Errors lists what could not be read. Each is reported as drift: a
	// setting that cannot be read cannot be shown to hold.
	Errors []string `json:"errors,omitempty"`
}

// Read collects the live values. readFile reads a file, run returns the
// trimmed stdout of a command.
func Read(readFile func(string) ([]byte, error), run func(name string, args ...string) (string, error)) Live {
	l := Live{Sysctls: map[string]string{}}
	for _, s := range Sysctls {
		b, err := readFile(s.Path)
		if err != nil {
			l.Errors = append(l.Errors, fmt.Sprintf("cannot read %s (%s): %v", s.Key, s.Path, err))
			continue
		}
		l.Sysctls[s.Key] = strings.TrimSpace(string(b))
	}
	b, err := readFile(SwapsPath)
	if err != nil {
		l.Errors = append(l.Errors, fmt.Sprintf("cannot read active swap (%s): %v", SwapsPath, err))
	} else {
		l.SwapDevices = countSwapAreas(string(b))
	}
	state, err := run("systemctl", "show", "-p", "LoadState", "--value", ApportUnit)
	if err != nil {
		l.Errors = append(l.Errors, fmt.Sprintf("cannot read whether %s is installed: %v", ApportUnit, err))
	} else {
		l.ApportLoadState = strings.TrimSpace(state)
	}
	return l
}

// countSwapAreas counts the rows of /proc/swaps below its header line.
func countSwapAreas(swaps string) int {
	n := 0
	for i, line := range strings.Split(swaps, "\n") {
		if i > 0 && strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// Drift lists every hardened setting whose live value differs from what
// install set, each as a sentence naming the setting, what it should be and
// what it is. Empty means the hardening holds.
func (l Live) Drift() []string {
	var drift []string
	for _, s := range Sysctls {
		if got, ok := l.Sysctls[s.Key]; ok && got != s.Want {
			drift = append(drift, fmt.Sprintf("%s is %q, install set %q", s.Key, shown(got), s.Want))
		}
	}
	if l.SwapDevices > 0 {
		drift = append(drift, "swap is in use ("+strconv.Itoa(l.SwapDevices)+" active area(s)), install turned it off")
	}
	if l.ApportLoadState != "" && !ApportDisabled(l.ApportLoadState) {
		drift = append(drift, fmt.Sprintf("%s is %q, install masked it", ApportUnit, l.ApportLoadState))
	}
	return append(drift, l.Errors...)
}

// maxShownValue bounds a live value quoted in a drift sentence: the value comes
// from the node's own report and ends up in alert text.
const maxShownValue = 120

// shown is v cut to maxShownValue bytes, marked when cut.
func shown(v string) string {
	if len(v) <= maxShownValue {
		return v
	}
	return v[:maxShownValue] + "…"
}
