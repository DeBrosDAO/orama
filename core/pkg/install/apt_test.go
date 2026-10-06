package install

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// Every apt-get the installer runs waits for a dpkg lock another apt holds: a
// fresh image's unattended-upgrades holds it on first boot, and the install of
// a new node failed at once with "Could not get lock".
func TestAptCommand_waitsForTheDpkgLock(t *testing.T) {
	cmd := aptCommand("install", "-y", "make")
	if cmd.Path != aptGetPath {
		t.Fatalf("path %q, want %q", cmd.Path, aptGetPath)
	}
	want := []string{aptGetPath, "-o", "DPkg::Lock::Timeout=900", "install", "-y", "make"}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args %q, want %q", cmd.Args, want)
	}
	if !slices.Contains(cmd.Env, "DEBIAN_FRONTEND=noninteractive") {
		t.Fatalf("env %q lacks DEBIAN_FRONTEND=noninteractive", cmd.Env)
	}
}

// No installer source calls apt-get except through aptCommand.
func TestInstallSources_runAptOnlyThroughAptCommand(t *testing.T) {
	for _, f := range []string{"checks.go", "prebuilt.go", "firewall.go", "wireguard.go", "nss_systemd.go"} {
		src := readSource(t, f)
		if strings.Contains(src, `exec.Command("apt-get"`) || strings.Contains(src, "exec.Command(aptGetPath") {
			t.Errorf("%s runs apt-get without the dpkg lock wait", f)
		}
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
