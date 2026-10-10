package hardening

import (
	"errors"
	"strings"
	"testing"
)

const swapsHeader = "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n"

func healthyFiles() map[string]string {
	return map[string]string{
		"/proc/sys/fs/suid_dumpable":         "0\n",
		"/proc/sys/kernel/core_pattern":      "|/bin/false\n",
		"/proc/sys/kernel/yama/ptrace_scope": "1\n",
		SwapsPath:                            swapsHeader,
	}
}

func readerOf(files map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		v, ok := files[p]
		if !ok {
			return nil, errors.New("no such file")
		}
		return []byte(v), nil
	}
}

func runnerOf(loadState string, err error) func(string, ...string) (string, error) {
	return func(string, ...string) (string, error) { return loadState, err }
}

func TestDrift_healthyNodeHasNone(t *testing.T) {
	for _, state := range []string{"not-found", "masked"} {
		l := Read(readerOf(healthyFiles()), runnerOf(state, nil))
		if d := l.Drift(); len(d) != 0 {
			t.Fatalf("%s: drift %v on a hardened node", state, d)
		}
	}
}

func TestDrift_namesEachDriftedSetting(t *testing.T) {
	files := healthyFiles()
	files["/proc/sys/fs/suid_dumpable"] = "2\n"
	files["/proc/sys/kernel/core_pattern"] = "|/usr/share/apport/apport %p\n"
	files[SwapsPath] = swapsHeader + "/swapfile file 1048572 0 -2\n"
	d := Read(readerOf(files), runnerOf("loaded", nil)).Drift()
	if len(d) != 4 {
		t.Fatalf("drift = %v, want suid_dumpable, core_pattern, swap and apport", d)
	}
	for i, want := range []string{"fs.suid_dumpable", "kernel.core_pattern", "swap is in use", ApportUnit} {
		if !strings.Contains(d[i], want) {
			t.Errorf("drift[%d] = %q, want it to name %q", i, d[i], want)
		}
	}
}

func TestDrift_unreadableSettingIsDriftNotSilence(t *testing.T) {
	files := healthyFiles()
	delete(files, "/proc/sys/fs/suid_dumpable")
	delete(files, SwapsPath)
	d := Read(readerOf(files), runnerOf("", errors.New("systemctl: exit status 1"))).Drift()
	if len(d) != 3 {
		t.Fatalf("drift = %v, want one per unreadable source", d)
	}
}

func TestDrift_zeroValueLiveReportsNothingToCompare(t *testing.T) {
	if d := (Live{}).Drift(); len(d) != 0 {
		t.Fatalf("an empty Live drifted: %v", d)
	}
}

func TestCountSwapAreas_edges(t *testing.T) {
	cases := map[string]int{"": 0, swapsHeader: 0, swapsHeader + "\n": 0, swapsHeader + "/a file 1 0 -2\n/dev/zram0 partition 1 0 100\n": 2}
	for in, want := range cases {
		if got := countSwapAreas(in); got != want {
			t.Errorf("countSwapAreas(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestDropIn_setsEverySysctlOnce(t *testing.T) {
	out := DropIn()
	if !strings.HasSuffix(out, "\n") {
		t.Error("the drop-in must end in a newline")
	}
	for _, s := range Sysctls {
		if line := s.Key + " = " + s.Want + "\n"; strings.Count(out, line) != 1 {
			t.Errorf("drop-in lacks exactly one %q", line)
		}
	}
}

// A live value comes from the node's own report and lands in alert text, so a
// drift sentence quotes at most maxShownValue bytes of it.
func TestDrift_longValueIsCut(t *testing.T) {
	l := Live{Sysctls: map[string]string{"kernel.core_pattern": strings.Repeat("x", 5000)}}
	for _, d := range l.Drift() {
		if len(d) > 300 {
			t.Fatalf("a drift sentence is %d bytes", len(d))
		}
	}
}
