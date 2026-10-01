package install

import (
	"strings"
	"testing"
)

// ramHygieneSettings parses a sysctl.d file the way sysctl -p reads it.
func ramHygieneSettings(t *testing.T, content string) map[string]string {
	t.Helper()
	settings := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("line %q is not key = value", line)
		}
		key = strings.TrimSpace(key)
		if _, dup := settings[key]; dup {
			t.Fatalf("%s is set twice", key)
		}
		settings[key] = strings.TrimSpace(value)
	}
	return settings
}

func TestRAMHygieneSysctl_restrictsPtraceOnDebian(t *testing.T) {
	got := ramHygieneSettings(t, ramHygieneSysctl)
	if got["kernel.yama.ptrace_scope"] != "1" {
		t.Errorf("kernel.yama.ptrace_scope = %q, want 1 (Debian's default is 0)", got["kernel.yama.ptrace_scope"])
	}
}

func TestRAMHygieneSysctl_disablesSuidDumps(t *testing.T) {
	got := ramHygieneSettings(t, ramHygieneSysctl)
	if got["fs.suid_dumpable"] != "0" {
		t.Errorf("fs.suid_dumpable = %q, want 0", got["fs.suid_dumpable"])
	}
}

func TestRAMHygieneSysctl_setsNothingElse(t *testing.T) {
	if got := ramHygieneSettings(t, ramHygieneSysctl); len(got) != 2 {
		t.Errorf("the drop-in sets %v, want exactly suid_dumpable and ptrace_scope", got)
	}
	if !strings.HasSuffix(ramHygieneSysctl, "\n") {
		t.Error("the drop-in must end in a newline")
	}
}
