//go:build e2e_fleet

package securityaudit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// unitAccounts is the account each always-running unit runs as
// (docs/SECURITY.md "Dedicated User", "Per-service accounts";
// docs/ARCHITECTURE.md "Process Isolation": Tor as debian-tor, WireGuard
// root). The SFU (orama-sfu) runs only with WebRTC and is checked where
// present.
var unitAccounts = map[string]string{
	"orama-node.service":                  "orama",
	edge.IndexGatewayUnit:                 "orama",
	edge.IndexRQLiteUnit:                  "orama",
	"orama-namespace-olric@index.service": "orama",
	edge.CaddyUnit:                        "orama",
	edge.TorUnit:                          "debian-tor",
}

// TestIsolation_everyUnitRunsAsItsAccount: no daemon runs as root except
// the ones documented to, and each runs as its own account; CoreDNS's is
// checked by dns-tls on nameservers, and any running SFU is orama-sfu.
func TestIsolation_everyUnitRunsAsItsAccount(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		for unit, want := range unitAccounts {
			if got := edge.ProcessUser(t, f, n, edge.MainPID(t, f, n, unit)); got != want {
				t.Errorf("%s: %s runs as %q, want %s", n.Name, unit, got, want)
			}
		}
		out := f.Exec(t, n, "for u in $(systemctl list-units --no-legend --state=running 'orama-namespace-sfu@*' | awk '{print $1}'); do ps -o user:32= -p $(systemctl show -p MainPID --value $u); done").Stdout
		for _, u := range strings.Fields(out) {
			if u != "orama-sfu" {
				t.Errorf("%s: an SFU runs as %q, want orama-sfu", n.Name, u)
			}
		}
	}
}

// What the shell prints when the kernel refuses to open another process's
// /proc/<pid>/environ: "Permission denied" where the entry is visible but
// protected, "No such file" (dash) or "No such file or directory" (bash) where the unit's ProtectProc=invisible
// hides other accounts' processes altogether (the CoreDNS unit does). Either
// way the read fails at the kernel, not in the shell or for a missing tool.
var environRefusals = []string{"Permission denied", "No such file"}

func refusedByKernel(stderr string) bool {
	for _, r := range environRefusals {
		if strings.Contains(stderr, r) {
			return true
		}
	}
	return false
}

// readEnviron counts target's /proc environ bytes as user from inside from's
// mount namespace (as that unit's process would read it), and returns the
// command's output: the count on stdout, the refusal on stderr.
func readEnviron(t *testing.T, f *fleet.Fleet, n fleet.Node, from, user string, target int) fleet.Output {
	t.Helper()
	pid := edge.MainPID(t, f, n, from)
	cmd := fmt.Sprintf("nsenter -t %d -m -- setpriv --reuid=%s --regid=%s --init-groups sh -c 'wc -c < /proc/%d/environ'", pid, user, user, target)
	return f.Exec(t, n, cmd)
}

// requireNamespaceTools fails unless n has nsenter and setpriv: without them
// every read "fails", and the negative half would pass having tested nothing.
func requireNamespaceTools(t *testing.T, f *fleet.Fleet, n fleet.Node) {
	t.Helper()
	if out := f.Exec(t, n, "command -v nsenter && command -v setpriv"); out.Exit != 0 {
		t.Fatalf("%s: nsenter or setpriv is not installed (exit %d), so the environ boundary cannot be tested: %s",
			n.Name, out.Exit, f.Redact(out.Stderr))
	}
}

// TestIsolation_environAcrossAccounts is a KNOWN GAP recorded as it stands:
// every daemon that runs as orama can read every other orama daemon's
// environment (docs/SECURITY.md "Per-service accounts": "a process that
// shares a uid with another passes the kernel's ptrace check ... Every
// daemon that runs as orama can therefore read what every other orama daemon
// holds"; "Not done yet: ... a runtime test ... that each unit cannot read
// another's /proc/<pid>/environ"). So Caddy reads the cluster gateway's
// environment. The isolated account does not: CoreDNS is refused by the
// kernel (a non-zero exit saying Permission denied, or No such file where the
// unit hides other processes with ProtectProc=invisible, not any failure). When
// the gap is closed this test fails on its first half; update it and
// SECURITY.md together.
func TestIsolation_environAcrossAccounts(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := edge.Nameservers(f)[0]
	requireNamespaceTools(t, f, n)
	gateway := edge.MainPID(t, f, n, edge.IndexGatewayUnit)
	caddy := readEnviron(t, f, n, edge.CaddyUnit, "orama", gateway)
	var size int
	_, err := fmt.Sscan(strings.TrimSpace(caddy.Stdout), &size)
	if caddy.Exit != 0 || err != nil || size == 0 {
		t.Errorf("KNOWN GAP changed: Caddy (orama) can no longer read the cluster gateway's environ (exit %d, %d bytes, %v: %s) — "+
			"update docs/SECURITY.md \"Per-service accounts\" and this test", caddy.Exit, size, err, f.Redact(caddy.Stderr))
	}
	coredns := readEnviron(t, f, n, edge.CoreDNSUnit, "orama-coredns", gateway)
	if coredns.Exit == 0 || !refusedByKernel(coredns.Stderr) {
		t.Errorf("CoreDNS (orama-coredns) reading the cluster gateway's environ: exit %d, stdout %q, stderr %q; want a non-zero exit with one of %q",
			coredns.Exit, strings.TrimSpace(coredns.Stdout), f.Redact(coredns.Stderr), environRefusals)
	}
}

// TestIsolation_noGlobalIPv6: IPv6 is off at runtime, so no interface has a
// global IPv6 address a service bound to :: could be reached on around the
// IPv4 firewall (docs/SECURITY.md "IPv6 Disabled"; the TCP scan of the
// public IPv4 addresses is wireguard-firewall's).
func TestIsolation_noGlobalIPv6(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		if out := f.MustExec(t, n, "ip -6 -o addr show scope global").Stdout; strings.TrimSpace(out) != "" {
			t.Errorf("%s has global IPv6 addresses: %s", n.Name, out)
		}
		if v := strings.TrimSpace(f.MustExec(t, n, "sysctl -n net.ipv6.conf.all.disable_ipv6").Stdout); v != "1" {
			t.Errorf("%s: net.ipv6.conf.all.disable_ipv6 = %s", n.Name, v)
		}
	}
}

// TestIsolation_kernelHardening: core dumps of setuid programs off and
// ptrace restricted to descendants (docs/SECURITY.md "RAM-to-disk":
// fs.suid_dumpable=0 and kernel.yama.ptrace_scope=1, both written by the
// installer's sysctl drop-in; the environ boundary above relies on the second).
func TestIsolation_kernelHardening(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		out := f.MustExec(t, n, "sysctl -n fs.suid_dumpable kernel.yama.ptrace_scope; swapon --noheadings | wc -l").Stdout
		v := strings.Fields(out)
		if len(v) != 3 || v[0] != "0" || v[1] == "0" || v[2] != "0" {
			t.Errorf("%s: suid_dumpable, ptrace_scope, swap devices = %v, want 0, >=1, 0", n.Name, v)
		}
	}
}
