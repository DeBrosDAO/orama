package report

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withProc(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldRoot, oldWindow := procRoot, stealSampleWindow
	procRoot, stealSampleWindow = dir, time.Millisecond
	t.Cleanup(func() { procRoot, stealSampleWindow = oldRoot, oldWindow })
}

func TestReadCPUTimes_totalExcludesGuest(t *testing.T) {
	withProc(t, map[string]string{"stat": "cpu  10 0 10 60 0 0 0 20 99 99\ncpu0 1 2 3\n"})
	got, ok := readCPUTimes()
	if !ok || got.total != 100 || got.steal != 20 {
		t.Fatalf("times = %+v ok=%v, want total 100 steal 20", got, ok)
	}
}

func TestReadCPUTimes_malformed(t *testing.T) {
	for _, stat := range []string{"", "cpu 1 2 3\n", "intr 1 2 3 4 5 6 7 8 9\n", "cpu 1 2 3 x 5 6 7 8\n"} {
		withProc(t, map[string]string{"stat": stat})
		if _, ok := readCPUTimes(); ok {
			t.Errorf("stat %q parsed", stat)
		}
	}
}

func TestSampleSteal_unreadableIsZero(t *testing.T) {
	withProc(t, map[string]string{})
	if got := sampleSteal(); got != 0 {
		t.Fatalf("steal = %v, want 0 when /proc/stat cannot be read", got)
	}
}

func TestReadPressure_someAvg60(t *testing.T) {
	withProc(t, map[string]string{"pressure/cpu": "some avg10=79.78 avg60=75.22 avg300=68.72 total=744\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"})
	if got := readPressure("cpu"); got != 75.22 {
		t.Fatalf("pressure = %v, want 75.22", got)
	}
}

func TestReadPressure_missingIsUnknown(t *testing.T) {
	withProc(t, map[string]string{})
	if got := readPressure("io"); got != pressureUnknown {
		t.Fatalf("pressure = %v, want unknown", got)
	}
}

func TestSocketBindEnforced_needsBPFFramework(t *testing.T) {
	old := systemdFeatures
	t.Cleanup(func() { systemdFeatures = old })
	for out, want := range map[string]bool{
		"systemd 259 (259.5)\n+PAM +AUDIT +BPF_FRAMEWORK +XKBCOMMON":  true,
		"systemd 252 (252.39)\n+PAM +AUDIT -BPF_FRAMEWORK +XKBCOMMON": false,
	} {
		systemdFeatures = func() (string, error) { return out, nil }
		if got := socketBindEnforced(); got != want {
			t.Errorf("features %q: enforced = %v, want %v", out, got, want)
		}
	}
	systemdFeatures = func() (string, error) { return "", os.ErrNotExist }
	if socketBindEnforced() {
		t.Error("an unreadable systemd version read as enforcing")
	}
}
