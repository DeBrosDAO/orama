package install

import (
	"fmt"
	"strconv"
	"strings"
)

// Profile is how much of the network a machine takes part in, and so how much
// machine it needs.
type Profile string

const (
	// ProfileClusterOnly runs a cluster node and nothing of the global layer.
	ProfileClusterOnly Profile = "cluster-only"
	// ProfileFull runs the cluster node and the global layer beside it: the
	// chain, the public IPFS and its provider, a relay.
	ProfileFull Profile = "full"
)

// bytesPerGiB is the unit of the floors and of what a machine reports.
const bytesPerGiB = 1024 * 1024 * 1024

// Floors of the full profile. The chain's GOMEMLIMIT already notes that 4 GB
// with the public IPFS and the namespaces ran out of memory, so 8 GiB; the 80 GiB
// is the chain, the cluster and the system, and the storage the operator offers
// is added to it (HardwareFloor.DiskBytes).
const (
	fullMinCPUCores  = 4
	fullMinRAMBytes  = 8 * bytesPerGiB
	fullBaseDiskByte = 80 * bytesPerGiB
)

// Hardware is what a machine has: its CPU threads, its RAM, and the free space
// of the disk the node's data lives on.
type Hardware struct {
	CPUCores      int
	RAMBytes      uint64
	FreeDiskBytes uint64
}

// HardwareFloor is the least a profile runs on.
type HardwareFloor struct {
	CPUCores  int
	RAMBytes  uint64
	DiskBytes uint64
}

// FloorFor is the floor of a profile. storageGB is the public storage the
// operator will offer (--storage-gb); it counts only for the full profile.
func FloorFor(profile Profile, storageGB uint64) (HardwareFloor, error) {
	switch profile {
	case ProfileClusterOnly:
		return HardwareFloor{CPUCores: MinCPUCores, RAMBytes: MinRAMBytes, DiskBytes: MinFreeDiskBytes}, nil
	case ProfileFull:
		return HardwareFloor{
			CPUCores: fullMinCPUCores, RAMBytes: fullMinRAMBytes,
			DiskBytes: fullBaseDiskByte + storageGB*bytesPerGB,
		}, nil
	}
	return HardwareFloor{}, fmt.Errorf("unknown profile %q (want %s or %s)", profile, ProfileFull, ProfileClusterOnly)
}

// bytesPerGB is the decimal gigabyte the storage budget is counted in (Kubo's
// StorageMax counts in it).
const bytesPerGB = 1_000_000_000

// CheckHardware refuses a machine below the profile's floor. Every shortfall is
// named, with what the machine has and what the profile needs, so one message
// says everything to change.
func CheckHardware(profile Profile, storageGB uint64, hw Hardware) error {
	floor, err := FloorFor(profile, storageGB)
	if err != nil {
		return err
	}
	var short []string
	if hw.CPUCores < floor.CPUCores {
		short = append(short, fmt.Sprintf("%d vCPU (needs %d)", hw.CPUCores, floor.CPUCores))
	}
	if hw.RAMBytes < floor.RAMBytes {
		short = append(short, fmt.Sprintf("%s RAM (needs %s)", gib(hw.RAMBytes), gib(floor.RAMBytes)))
	}
	if hw.FreeDiskBytes < floor.DiskBytes {
		short = append(short, fmt.Sprintf("%s free disk (needs %s)", gib(hw.FreeDiskBytes), gib(floor.DiskBytes)))
	}
	if len(short) == 0 {
		return nil
	}
	return fmt.Errorf("the machine is below the %s profile: %s%s", profile, strings.Join(short, ", "), profileAdvice(profile))
}

func profileAdvice(profile Profile) string {
	if profile == ProfileFull {
		return "; use a larger machine, lower --storage-gb, or run the cluster alone with --cluster-only"
	}
	return "; use a larger machine"
}

func gib(b uint64) string {
	return strconv.FormatFloat(float64(b)/bytesPerGiB, 'f', 1, 64) + " GiB"
}

// ParseHardware reads what HardwareProbeCommand printed: three lines
// `cpu=<threads>`, `ram_kb=<MemTotal>` and `disk_kb=<free KiB of /opt>`, among
// any others. A line that is missing or not a number is an error, never a zero that would read as
// a machine with nothing.
func ParseHardware(out string) (Hardware, error) {
	values := map[string]uint64{}
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		// Other lines of the probe's output are not this function's.
		if !ok || (key != "cpu" && key != "ram_kb" && key != "disk_kb") {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
		if err != nil {
			return Hardware{}, fmt.Errorf("hardware probe: %s=%q is not a number", key, val)
		}
		values[key] = n
	}
	for _, key := range []string{"cpu", "ram_kb", "disk_kb"} {
		if _, ok := values[key]; !ok {
			return Hardware{}, fmt.Errorf("hardware probe: no %s in %q", key, shown(out))
		}
	}
	// A machine with no CPU or no memory is a broken probe; a disk with no free
	// space is a real answer, and one the floor refuses by name.
	if values["cpu"] == 0 || values["ram_kb"] == 0 {
		return Hardware{}, fmt.Errorf("hardware probe: cpu=%d ram_kb=%d in %q", values["cpu"], values["ram_kb"], shown(out))
	}
	return Hardware{CPUCores: int(values["cpu"]), RAMBytes: values["ram_kb"] * 1024, FreeDiskBytes: values["disk_kb"] * 1024}, nil
}

// shownProbe is how much of a probe's output an error repeats.
const shownProbe = 200

func shown(out string) string {
	out = strings.TrimSpace(out)
	if len(out) > shownProbe {
		return out[:shownProbe] + "..."
	}
	return out
}

// HardwareProbeCommand is the shell snippet that prints what ParseHardware
// reads, on the machine itself. The disk is the one /opt lives on, where
// /opt/orama and the state directories are (df of a path that does not exist yet
// would fail, so it walks up to one that does).
const HardwareProbeCommand = `echo cpu=$(nproc); awk '/^MemTotal:/ {print "ram_kb=" $2}' /proc/meminfo; ` +
	`d=/opt; while [ ! -e "$d" ]; do d=$(dirname "$d"); done; df -Pk "$d" | awk 'NR==2 {print "disk_kb=" $4}'`
