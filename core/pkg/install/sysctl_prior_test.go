package install

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/hardening"
)

// fakeFiles is a readFile over a map; a missing path is fs.ErrNotExist.
func fakeFiles(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		if v, ok := files[path]; ok {
			return []byte(v), nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestWithPriorValues_newDropInRecordsTheLiveValues(t *testing.T) {
	read := fakeFiles(map[string]string{
		IPv6DisableSysctls[0].Path: "0\n",
		IPv6DisableSysctls[1].Path: "0\n",
	})
	got, err := withPriorValues(IPv6DisableSysctlPath, ipv6DisableDropIn(), IPv6DisableSysctls, read)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"net.ipv6.conf.all.disable_ipv6 = 1\n",
		PriorSysctlPrefix + "net.ipv6.conf.all.disable_ipv6=0\n",
		PriorSysctlPrefix + "net.ipv6.conf.default.disable_ipv6=0\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("drop-in lacks %q:\n%s", want, got)
		}
	}
}

// A second install finds Orama's own values in the kernel; it must keep what the first
// recorded, not record those.
func TestWithPriorValues_existingDropInKeepsItsRecord(t *testing.T) {
	existing := ipv6DisableDropIn() + PriorSysctlPrefix + "net.ipv6.conf.all.disable_ipv6=0\n"
	read := fakeFiles(map[string]string{
		IPv6DisableSysctlPath:      existing,
		IPv6DisableSysctls[0].Path: "1\n",
		IPv6DisableSysctls[1].Path: "1\n",
	})
	got, err := withPriorValues(IPv6DisableSysctlPath, ipv6DisableDropIn(), IPv6DisableSysctls, read)
	if err != nil {
		t.Fatal(err)
	}
	if got != existing {
		t.Errorf("got:\n%s\nwant the existing record unchanged:\n%s", got, existing)
	}
}

// A drop-in an older install wrote has no record, and the live values are Orama's: record nothing.
func TestWithPriorValues_existingDropInWithoutRecordStaysWithout(t *testing.T) {
	read := fakeFiles(map[string]string{
		IPv6DisableSysctlPath:      ipv6DisableDropIn(),
		IPv6DisableSysctls[0].Path: "1\n",
	})
	got, err := withPriorValues(IPv6DisableSysctlPath, ipv6DisableDropIn(), IPv6DisableSysctls, read)
	if err != nil {
		t.Fatal(err)
	}
	if got != ipv6DisableDropIn() {
		t.Errorf("a record appeared:\n%s", got)
	}
}

func TestWithPriorValues_keyTheKernelLacksIsSkipped(t *testing.T) {
	read := fakeFiles(map[string]string{IPv6DisableSysctls[1].Path: "0\n"})
	got, err := withPriorValues(IPv6DisableSysctlPath, ipv6DisableDropIn(), IPv6DisableSysctls, read)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "net.ipv6.conf.all.disable_ipv6=") || !strings.Contains(got, "net.ipv6.conf.default.disable_ipv6=0") {
		t.Errorf("want only the key the kernel has recorded:\n%s", got)
	}
}

func TestWithPriorValues_readErrorIsReturned(t *testing.T) {
	boom := errors.New("permission denied")
	read := func(path string) ([]byte, error) {
		if path == IPv6DisableSysctlPath {
			return nil, fs.ErrNotExist
		}
		return nil, boom
	}
	if _, err := withPriorValues(IPv6DisableSysctlPath, ipv6DisableDropIn(), IPv6DisableSysctls, read); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the read error", err)
	}
	read = func(string) ([]byte, error) { return nil, boom }
	if _, err := withPriorValues(IPv6DisableSysctlPath, ipv6DisableDropIn(), IPv6DisableSysctls, read); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the drop-in read error", err)
	}
}

func TestWithPriorValues_ramHygieneRecordsEveryHardenedKey(t *testing.T) {
	files := map[string]string{
		"/proc/sys/fs/suid_dumpable":        "2\n",
		"/proc/sys/kernel/core_pattern":     "|/usr/share/apport/apport -p%p %s\n",
		"/proc/sys/kernel/yama/ptrace_scope": "0\n",
	}
	got, err := withPriorValues(RAMHygieneSysctlPath, ramHygieneSysctl, hardening.Sysctls, fakeFiles(files))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		PriorSysctlPrefix + "fs.suid_dumpable=2\n",
		PriorSysctlPrefix + "kernel.core_pattern=|/usr/share/apport/apport -p%p %s\n",
		PriorSysctlPrefix + "kernel.yama.ptrace_scope=0\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("drop-in lacks %q:\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, ramHygieneSysctl) {
		t.Error("the drop-in must start with the hardening settings")
	}
}

func TestIPv6DisableDropIn_setsExactlyTheExportedKeys(t *testing.T) {
	got := ramHygieneSettings(t, ipv6DisableDropIn())
	if len(got) != len(IPv6DisableSysctls) {
		t.Fatalf("drop-in sets %v, want %d keys", got, len(IPv6DisableSysctls))
	}
	for _, s := range IPv6DisableSysctls {
		if got[s.Key] != "1" {
			t.Errorf("%s = %q, want 1", s.Key, got[s.Key])
		}
	}
}

// The drop-in must record the kernel's IPv6 values from before the rules ran: the rules set
// disable_ipv6=1 live, and a record taken after them made the wipe turn IPv6 off again.
func TestApplySystemRules_recordsIPv6BeforeTheRulesChangeIt(t *testing.T) {
	disabled := false
	run := func(name string, args ...string) error {
		if name == "sysctl" && strings.Join(args, " ") == "-w net.ipv6.conf.all.disable_ipv6=1" {
			disabled = true
		}
		return nil
	}
	readFile := func(path string) ([]byte, error) {
		if path == IPv6DisableSysctlPath {
			return nil, fs.ErrNotExist
		}
		if disabled {
			return []byte("1\n"), nil
		}
		return []byte("0\n"), nil
	}
	rules := []string{"ufw allow 22/tcp", "sysctl -w net.ipv6.conf.all.disable_ipv6=1", "sysctl -w net.ipv6.conf.default.disable_ipv6=1"}
	dropIn, err := applySystemRules(rules, run, readFile)
	if err != nil {
		t.Fatal(err)
	}
	if !disabled {
		t.Fatal("the IPv6 rule did not run")
	}
	for _, s := range IPv6DisableSysctls {
		if want := PriorSysctlPrefix + s.Key + "=0"; !strings.Contains(dropIn, want) {
			t.Errorf("the drop-in does not record the value from before the rules (%q):\n%s", want, dropIn)
		}
	}
}

func TestApplySystemRules_aRuleThatFailsIsAnError(t *testing.T) {
	boom := errors.New("ufw refused")
	_, err := applySystemRules([]string{"ufw default deny incoming"}, func(string, ...string) error { return boom },
		func(string) ([]byte, error) { return nil, fs.ErrNotExist })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the rule's failure", err)
	}
}
