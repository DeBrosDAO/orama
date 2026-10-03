//go:build e2e_fleet

package install

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// sysctlValue reads one live kernel setting.
func sysctlValue(t testing.TB, f *fleet.Fleet, n fleet.Node, key string) string {
	t.Helper()
	return strings.TrimSpace(f.MustExec(t, n, "sysctl -n "+key).Stdout)
}

// requireContains fails unless the file on n contains every line.
func requireContains(t testing.TB, f *fleet.Fleet, n fleet.Node, path string, lines ...string) {
	t.Helper()
	body := string(f.ReadFile(t, n, path))
	for _, l := range lines {
		if !strings.Contains(body, l) {
			t.Errorf("%s: %s lacks %q:\n%s", n.Name, path, l, body)
		}
	}
}

// TestInstall_ipv6DisabledPersistently: IPv6 is off in the running kernel
// and on every boot (docs/SECURITY.md "IPv6 Disabled", core/pkg/install
// firewall.go persistIPv6Disable).
func TestInstall_ipv6DisabledPersistently(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		for _, key := range []string{"net.ipv6.conf.all.disable_ipv6", "net.ipv6.conf.default.disable_ipv6"} {
			if v := sysctlValue(t, f, n, key); v != "1" {
				t.Errorf("%s: %s = %s, want 1", n.Name, key, v)
			}
		}
		requireContains(t, f, n, infra.IPv6SysctlConf,
			"net.ipv6.conf.all.disable_ipv6 = 1", "net.ipv6.conf.default.disable_ipv6 = 1")
		if out := f.Exec(t, n, "ip -6 -o addr show scope global"); strings.TrimSpace(out.Stdout) != "" {
			t.Errorf("%s still has a global IPv6 address:\n%s", n.Name, out.Stdout)
		}
	}
}

// TestInstall_ramHygiene: no swap, swap.target masked, suid core dumps off,
// systemd-coredump keeps nothing (docs/SECURITY.md "RAM-to-disk").
func TestInstall_ramHygiene(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		if out := f.MustExec(t, n, "swapon --noheadings --show"); strings.TrimSpace(out.Stdout) != "" {
			t.Errorf("%s has swap enabled:\n%s", n.Name, out.Stdout)
		}
		if s := strings.TrimSpace(f.Exec(t, n, "systemctl is-enabled swap.target").Stdout); s != "masked" {
			t.Errorf("%s: swap.target is %q, want masked", n.Name, s)
		}
		if v := sysctlValue(t, f, n, "fs.suid_dumpable"); v != "0" {
			t.Errorf("%s: fs.suid_dumpable = %s, want 0", n.Name, v)
		}
		// apport's start writes fs.suid_dumpable=2 after the sysctl at boot
		// (core/pkg/install apportUnit): masked, or not shipped (Debian).
		if s := strings.TrimSpace(f.Exec(t, n, "systemctl show -p LoadState --value apport.service").Stdout); s != "masked" && s != "not-found" {
			t.Errorf("%s: apport.service LoadState is %q, want masked or not-found", n.Name, s)
		}
		requireContains(t, f, n, infra.RAMHygieneConf, "fs.suid_dumpable = 0")
		requireContains(t, f, n, infra.CoredumpConf, "Storage=none", "ProcessSizeMax=0")
	}
}

// TestInstall_secretBearingUnitsNeverSwap: the units that hold secrets run
// with MemorySwapMax=0 (docs/SECURITY.md "RAM-to-disk": the control that
// keeps secrets off the block device).
func TestInstall_secretBearingUnitsNeverSwap(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		for _, unit := range []string{infra.IndexRQLiteUnit, infra.IndexGatewayUnit} {
			v := strings.TrimSpace(f.MustExec(t, n, "systemctl show -p MemorySwapMax --value "+unit).Stdout)
			if v != "0" {
				t.Errorf("%s: %s MemorySwapMax=%s, want 0", n.Name, unit, v)
			}
		}
	}
}

// TestInstall_servicesRunAsOrama: the node supervisor and the index daemons
// run as the unprivileged orama user, never root (docs/SECURITY.md
// "Dedicated User").
func TestInstall_servicesRunAsOrama(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		for _, unit := range []string{infra.NodeUnit, infra.IndexRQLiteUnit, infra.IndexOlricUnit, infra.IndexGatewayUnit} {
			user := strings.TrimSpace(f.MustExec(t, n, "systemctl show -p User --value "+unit).Stdout)
			if user != "orama" {
				t.Errorf("%s: %s runs as %q, want orama", n.Name, unit, user)
			}
			pid := strings.TrimSpace(f.MustExec(t, n, "systemctl show -p MainPID --value "+unit).Stdout)
			if pid == "" || pid == "0" {
				t.Errorf("%s: %s has no main process", n.Name, unit)
				continue
			}
			owner := strings.TrimSpace(f.MustExec(t, n, "ps -o user= -p "+pid).Stdout)
			if owner != "orama" {
				t.Errorf("%s: %s's process %s runs as %q", n.Name, unit, pid, owner)
			}
		}
	}
}
