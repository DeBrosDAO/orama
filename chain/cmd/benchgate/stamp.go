package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// StableOS, StableArch and MinStableCPUs describe the box a release baseline is produced on:
	// a laptop or a small VM throttles and shares cores, so its numbers are not comparable run to
	// run.
	StableOS      = "linux"
	StableArch    = "amd64"
	MinStableCPUs = 8

	// stampPrefix starts the one comment line `make bench` puts at the top of a benchmark file.
	// benchstat ignores lines that are not results or `key: value` configuration, so the stamp
	// does not change what it compares.
	stampPrefix = "# benchgate:"
)

// ErrUnstableHost means the machine is not the stable box a release baseline needs.
var ErrUnstableHost = errors.New("this machine cannot produce a release baseline")

// ErrNoStamp means a benchmark file carries no stamp, so nothing says where it was measured.
var ErrNoStamp = errors.New("benchmark file has no benchgate stamp")

// Host is the machine a benchmark run is on.
type Host struct {
	OS   string
	Arch string
	CPUs int
}

// Stable reports whether the host is a stable 8-core Linux amd64 box.
func (h Host) Stable() bool {
	return h.OS == StableOS && h.Arch == StableArch && h.CPUs >= MinStableCPUs
}

// Stamp says where a benchmark file was measured and whether it may serve as a release baseline.
type Stamp struct {
	Host
	// Release is true only for a file `make bench-baseline` wrote on a stable host.
	Release bool
}

// NewStamp returns the stamp for a run on host. A run (baseline false) is never a release
// baseline. A baseline needs a stable host; allowUnstable writes it anyway, marked as not a
// release baseline.
func NewStamp(host Host, baseline, allowUnstable bool) (Stamp, error) {
	if !baseline {
		return Stamp{Host: host}, nil
	}
	if host.Stable() {
		return Stamp{Host: host, Release: true}, nil
	}
	if !allowUnstable {
		return Stamp{}, fmt.Errorf("%w: it is %s/%s with %d CPUs, a release baseline needs %s/%s with at least %d (pass BENCH_ALLOW_UNSTABLE=1 to write one marked as not a release baseline)",
			ErrUnstableHost, host.OS, host.Arch, host.CPUs, StableOS, StableArch, MinStableCPUs)
	}
	return Stamp{Host: host}, nil
}

// Line is the stamp as the comment line at the top of a benchmark file.
func (s Stamp) Line() string {
	return fmt.Sprintf("%s goos=%s goarch=%s cpus=%d stable=%t release=%t", stampPrefix, s.OS, s.Arch, s.CPUs, s.Stable(), s.Release)
}

// ParseStamp finds the stamp in a benchmark file. It returns ErrNoStamp when there is none.
func ParseStamp(r io.Reader) (Stamp, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(sc.Text(), stampPrefix)
		if !ok {
			continue
		}
		return parseStampFields(strings.Fields(rest))
	}
	if err := sc.Err(); err != nil {
		return Stamp{}, fmt.Errorf("read benchmark file: %w", err)
	}
	return Stamp{}, ErrNoStamp
}

func parseStampFields(fields []string) (Stamp, error) {
	var s Stamp
	for _, f := range fields {
		key, value, _ := strings.Cut(f, "=")
		var err error
		switch key {
		case "goos":
			s.OS = value
		case "goarch":
			s.Arch = value
		case "cpus":
			s.CPUs, err = strconv.Atoi(value)
		case "release":
			s.Release, err = strconv.ParseBool(value)
		}
		if err != nil {
			return Stamp{}, fmt.Errorf("benchgate stamp field %q: %w", f, err)
		}
	}
	return s, nil
}

// CheckProvenance decides whether base and fresh may be compared as a release gate: the baseline
// must be a release baseline, the new run must come from a stable host, and both must be the same
// OS and architecture. allowUnstable waives the first two (never the third: a different platform
// compares nothing).
func CheckProvenance(base, fresh Stamp, allowUnstable bool) error {
	if base.OS != fresh.OS || base.Arch != fresh.Arch {
		return fmt.Errorf("baseline was measured on %s/%s and the new run on %s/%s: they are not comparable", base.OS, base.Arch, fresh.OS, fresh.Arch)
	}
	if allowUnstable {
		return nil
	}
	if !base.Release {
		return errors.New("the baseline is not a release baseline (written off a stable host, or by make bench): write one with make bench-baseline on a stable box")
	}
	if !fresh.Stable() {
		return fmt.Errorf("the new run was measured on %s/%s with %d CPUs, not a stable box: its numbers cannot gate a release", fresh.OS, fresh.Arch, fresh.CPUs)
	}
	return nil
}
