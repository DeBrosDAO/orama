package decommission

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/hardening"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// sysctlMachine is a test machine for the restore block: a root with /proc/sys keys and
// sysctl.d drop-ins, and a sysctl that logs what it is asked to set.
type sysctlMachine struct {
	t    *testing.T
	root string
	log  string
}

func newSysctlMachine(t *testing.T) *sysctlMachine {
	t.Helper()
	return &sysctlMachine{t: t, root: t.TempDir(), log: filepath.Join(t.TempDir(), "sysctl.log")}
}

func (m *sysctlMachine) write(path, content string) {
	m.t.Helper()
	full := filepath.Join(m.root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		m.t.Fatal(err)
	}
}

// kernelHas makes /proc/sys/<key> exist, as it does on a kernel that has the setting.
func (m *sysctlMachine) kernelHas(keys ...string) {
	for _, k := range keys {
		m.write("/proc/sys/"+strings.ReplaceAll(k, ".", "/"), "1\n")
	}
}

// run executes the blocks in order with the machine's root in place of /proc/sys and /etc/sysctl.d,
// then the leftover loop for unrestored keys. failing names a key sysctl refuses to set.
func (m *sysctlMachine) run(failing string, blocks ...string) (calls []string, out string) {
	m.t.Helper()
	sysctl := "echo \"$*\" >> " + m.log + "\nexit 0"
	if failing != "" {
		sysctl = `case "$*" in *` + failing + `=*) exit 1;; esac` + "\n" + sysctl
	}
	bin := fakeBin(m.t, map[string]string{"sysctl": sysctl})
	script := strings.Join(blocks, "\n") + "\necho \"unrestored=[$sysctl_unrestored]\""
	script = strings.ReplaceAll(script, "/proc/sys/", m.root+"/proc/sys/")
	script = strings.ReplaceAll(script, "/etc/sysctl.d/", m.root+"/etc/sysctl.d/")
	cmd := exec.Command(bashPath(m.t), "-c", script)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	b, err := cmd.CombinedOutput()
	if err != nil {
		m.t.Fatalf("script failed: %v\n%s", err, b)
	}
	if logged, err := os.ReadFile(m.log); err == nil {
		calls = strings.Split(strings.TrimSpace(string(logged)), "\n")
	}
	return calls, string(b)
}

func (m *sysctlMachine) exists(path string) bool {
	_, err := os.Stat(filepath.Join(m.root, path))
	return err == nil
}

var ipv6Keys = sysctlKeys(install.IPv6DisableSysctls)

func TestIPv6Restore_putsBackTheRecordedValuesAndRemovesTheDropIn(t *testing.T) {
	m := newSysctlMachine(t)
	m.kernelHas(ipv6Keys...)
	m.write(install.IPv6DisableSysctlPath, "net.ipv6.conf.all.disable_ipv6 = 1\n"+
		install.PriorSysctlPrefix+"net.ipv6.conf.all.disable_ipv6=1\n"+
		install.PriorSysctlPrefix+"net.ipv6.conf.default.disable_ipv6=0\n")
	calls, out := m.run("", restoreSysctlsFunc(), ipv6RestoreBlock())
	want := []string{"-q -w net.ipv6.conf.all.disable_ipv6=1", "-q -w net.ipv6.conf.default.disable_ipv6=0"}
	if !slices.Equal(calls, want) {
		t.Errorf("sysctl calls %q, want %q", calls, want)
	}
	if m.exists(install.IPv6DisableSysctlPath) {
		t.Error("the IPv6 drop-in is still there")
	}
	if !strings.Contains(out, "unrestored=[]") {
		t.Errorf("output: %s", out)
	}
}

// An install that recorded nothing leaves IPv6 on the kernel default.
func TestIPv6Restore_withoutARecordUsesTheKernelDefault(t *testing.T) {
	m := newSysctlMachine(t)
	m.kernelHas(ipv6Keys...)
	m.write(install.IPv6DisableSysctlPath, "net.ipv6.conf.all.disable_ipv6 = 1\n")
	calls, _ := m.run("", restoreSysctlsFunc(), ipv6RestoreBlock())
	want := []string{"-q -w net.ipv6.conf.all.disable_ipv6=0", "-q -w net.ipv6.conf.default.disable_ipv6=0"}
	if !slices.Equal(calls, want) {
		t.Errorf("sysctl calls %q, want %q", calls, want)
	}
}

// A machine the drop-in is already gone from (a second wipe) is still set right and the rm is quiet.
func TestIPv6Restore_missingDropInStillRestoresTheDefault(t *testing.T) {
	m := newSysctlMachine(t)
	m.kernelHas(ipv6Keys...)
	calls, out := m.run("", restoreSysctlsFunc(), ipv6RestoreBlock())
	if len(calls) != 2 || !strings.HasSuffix(calls[0], "=0") {
		t.Errorf("sysctl calls %q, want both keys set to 0", calls)
	}
	if !strings.Contains(out, "unrestored=[]") {
		t.Errorf("output: %s", out)
	}
}

func TestIPv6Restore_skipsAKeyTheKernelLacks(t *testing.T) {
	m := newSysctlMachine(t)
	m.write(install.IPv6DisableSysctlPath, "")
	calls, out := m.run("", restoreSysctlsFunc(), ipv6RestoreBlock())
	if len(calls) != 0 || !strings.Contains(out, "unrestored=[]") {
		t.Errorf("a kernel without IPv6 was asked to set it: %q\n%s", calls, out)
	}
	if m.exists(install.IPv6DisableSysctlPath) {
		t.Error("the drop-in must go even where the kernel has no IPv6")
	}
}

// A value sysctl refuses is reported as a leftover, never swallowed.
func TestIPv6Restore_aKeyThatCannotBeSetIsReported(t *testing.T) {
	m := newSysctlMachine(t)
	m.kernelHas(ipv6Keys...)
	m.write(install.IPv6DisableSysctlPath, "")
	_, out := m.run("net.ipv6.conf.all.disable_ipv6", restoreSysctlsFunc(), ipv6RestoreBlock())
	if !strings.Contains(out, "unrestored=[ net.ipv6.conf.all.disable_ipv6]") {
		t.Errorf("output: %s", out)
	}
	check := leftoverCheck(false)
	if !strings.Contains(check, "for key in $sysctl_unrestored") {
		t.Error("the leftover check does not report keys that were not restored")
	}
}

func TestRAMHygieneRestore_putsBackRecordedValuesIncludingAPipedCorePattern(t *testing.T) {
	m := newSysctlMachine(t)
	var keys []string
	for _, s := range hardening.Sysctls {
		keys = append(keys, s.Key)
	}
	m.kernelHas(keys...)
	m.write(install.RAMHygieneSysctlPath, hardening.DropIn()+
		install.PriorSysctlPrefix+"fs.suid_dumpable=2\n"+
		install.PriorSysctlPrefix+"kernel.core_pattern=|/usr/share/apport/apport -p%p %s\n"+
		install.PriorSysctlPrefix+"kernel.yama.ptrace_scope=0\n")
	calls, _ := m.run("", restoreSysctlsFunc(), strings.TrimLeft(ramHygieneRestoreBlock(), " "))
	want := []string{
		"-q -w fs.suid_dumpable=2",
		"-q -w kernel.core_pattern=|/usr/share/apport/apport -p%p %s",
		"-q -w kernel.yama.ptrace_scope=0",
	}
	if !slices.Equal(calls, want) {
		t.Errorf("sysctl calls %q, want %q", calls, want)
	}
	if m.exists(install.RAMHygieneSysctlPath) {
		t.Error("the RAM hygiene drop-in is still there")
	}
}

// With no record there is nothing to put back: the drop-in goes and the next boot is stock.
func TestRAMHygieneRestore_withoutARecordOnlyRemovesTheDropIn(t *testing.T) {
	m := newSysctlMachine(t)
	for _, s := range hardening.Sysctls {
		m.kernelHas(s.Key)
	}
	m.write(install.RAMHygieneSysctlPath, hardening.DropIn())
	calls, _ := m.run("", restoreSysctlsFunc(), strings.TrimLeft(ramHygieneRestoreBlock(), " "))
	if len(calls) != 0 {
		t.Errorf("sysctl was called without a record: %q", calls)
	}
	if m.exists(install.RAMHygieneSysctlPath) {
		t.Error("the RAM hygiene drop-in is still there")
	}
}

// The plain wipe removes the IPv6 drop-in and restores IPv6, in that order and before the
// leftover check; only --nuclear touches the RAM hygiene drop-in.
func TestWipeScript_ipv6DropInIsRestoredAndRemovedByEveryWipe(t *testing.T) {
	for _, nuclear := range []bool{false, true} {
		script := removalPart(wipeScript(nuclear))
		restore := strings.Index(script, "restore_prior_sysctls "+install.IPv6DisableSysctlPath)
		remove := strings.Index(script, "rm -f "+install.IPv6DisableSysctlPath)
		if restore < 0 || remove < 0 || restore > remove {
			t.Errorf("nuclear=%v: want the IPv6 values restored (%d) before the drop-in is removed (%d)", nuclear, restore, remove)
		}
		if !strings.Contains(script, "restore_prior_sysctls() {") {
			t.Errorf("nuclear=%v: the restore function is not defined", nuclear)
		}
		if strings.Contains(script, "restore_prior_sysctls() {") && strings.Index(script, "restore_prior_sysctls() {") > restore {
			t.Errorf("nuclear=%v: the function is used before it is defined", nuclear)
		}
	}
}

func TestWipeScript_ramHygieneDropInIsRemovedOnlyByNuclear(t *testing.T) {
	script := removalPart(wipeScript(true))
	restore := strings.Index(script, "restore_prior_sysctls "+install.RAMHygieneSysctlPath)
	remove := strings.Index(script, "rm -f "+install.RAMHygieneSysctlPath)
	nuclear := strings.Index(script, `if [ -n "$NUCLEAR" ]`)
	if restore < nuclear || nuclear < 0 || remove < restore {
		t.Errorf("want the restore (%d) and removal (%d) inside the nuclear block (%d)", restore, remove, nuclear)
	}
	if strings.Contains(wipeScript(false), "NUCLEAR=1") || !strings.Contains(script, "NUCLEAR=1") {
		t.Error("the RAM hygiene drop-in must sit behind the NUCLEAR switch, which only --nuclear sets")
	}
	for _, s := range hardening.Sysctls {
		if !strings.Contains(script[restore:remove], s.Key) {
			t.Errorf("%s is not restored", s.Key)
		}
	}
}

func TestWipePaths_listTheSysctlDropIns(t *testing.T) {
	if !slices.Contains(wipedPaths(), install.IPv6DisableSysctlPath) {
		t.Error("the leftover check of a plain wipe does not list the IPv6 drop-in")
	}
	if slices.Contains(wipedPaths(), install.RAMHygieneSysctlPath) || slices.Contains(globalPaths(), install.RAMHygieneSysctlPath) {
		t.Error("a plain wipe must not require the RAM hygiene drop-in to be gone")
	}
	if !slices.Contains(nuclearPaths(), install.RAMHygieneSysctlPath) {
		t.Error("the leftover check of a nuclear wipe does not list the RAM hygiene drop-in")
	}
}

func TestWipeLeftoverCheck_reportsTheSysctlDropIns(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{install.IPv6DisableSysctlPath, install.RAMHygieneSysctlPath} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, code := runCheck(t, root, false, "", "Status: active\n", nil)
	if code == 0 || !strings.Contains(out, "LEFTOVER: "+root+install.IPv6DisableSysctlPath) || strings.Contains(out, install.RAMHygieneSysctlPath) {
		t.Errorf("a plain wipe must list only the IPv6 drop-in (exit %d):\n%s", code, out)
	}
	out, code = runCheck(t, root, true, "", "Status: active\n", nil)
	if code == 0 || !strings.Contains(out, "LEFTOVER: "+root+install.RAMHygieneSysctlPath) || !strings.Contains(out, "LEFTOVER: "+root+install.IPv6DisableSysctlPath) {
		t.Errorf("a nuclear wipe must list both drop-ins (exit %d):\n%s", code, out)
	}
}
