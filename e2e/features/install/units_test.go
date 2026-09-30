//go:build e2e_fleet

package install

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
)

// legacyHostUnits are the pre-namespace host units install removes on every
// install and upgrade (core/pkg/install/installers/host_units_legacy.go).
var legacyHostUnits = []string{
	"orama-ipfs-gc.timer", "orama-ipfs-gc.service", "orama-ipfs-cluster.service", "orama-ipfs.service",
	"orama-olric.service", "orama-vault.service", "coredns.service", "caddy.service", "ntfy.service",
	"orama-sni-router.service",
}

// installedUnitPatterns are the units install writes and the supervisor's
// platform instances: the host unit and every @index / @nameserver instance.
const installedUnitPatterns = "'orama-node.service' 'orama-privhelper.*' 'orama-namespace-*@index.service' 'orama-namespace-*@nameserver.service'"

// TestInstall_onlyTheNodeHostUnit: install writes one host unit,
// orama-node.service, enabled; everything else is an @index instance the
// supervisor starts (core/pkg/install/orchestrator.go Phase5). The overlay
// comes up at boot through orama-namespace-wireguard@index, and the leftover
// wg-quick@wg0 is disabled so the two never fight over wg0.
func TestInstall_onlyTheNodeHostUnit(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		enabled := func(unit string) string {
			return strings.TrimSpace(f.Exec(t, n, "systemctl is-enabled "+unit).Stdout)
		}
		if s := enabled(infra.NodeUnit); s != "enabled" {
			t.Errorf("%s: %s is %q, want enabled", n.Name, infra.NodeUnit, s)
		}
		if s := enabled(infra.WireGuardUnit); s != "enabled" {
			t.Errorf("%s: %s is %q, want enabled", n.Name, infra.WireGuardUnit, s)
		}
		if s := enabled(infra.LeftoverWireGuardUnit); s == "enabled" {
			t.Errorf("%s: the leftover %s is still enabled", n.Name, infra.LeftoverWireGuardUnit)
		}
		for _, u := range legacyHostUnits {
			if out := f.Exec(t, n, "test -e /etc/systemd/system/"+u); out.Exit == 0 {
				t.Errorf("%s: the legacy host unit %s is still installed", n.Name, u)
			}
		}
		for _, u := range []string{infra.NodeUnit, infra.IndexRQLiteUnit, infra.IndexOlricUnit, infra.IndexGatewayUnit, infra.WireGuardUnit} {
			if s := f.Unit(t, n, u); s != "active" {
				t.Errorf("%s: %s is %s", n.Name, u, s)
			}
		}
		// Only the units install writes: tenant namespaces the stages running
		// in parallel create and delete may fail on their own account.
		if out := f.Exec(t, n, "systemctl --failed --no-legend --plain "+installedUnitPatterns); strings.TrimSpace(out.Stdout) != "" {
			t.Errorf("%s has failed install units:\n%s", n.Name, out.Stdout)
		}
	}
}

// TestInstall_nameserversRunCoreDNS: a nameserver node runs CoreDNS under
// its own account, a plain node does not run it at all (docs/SECURITY.md
// "Per-service accounts").
func TestInstall_nameserversRunCoreDNS(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	const unit = infra.CoreDNSUnit
	for _, n := range f.State.Nodes {
		state := f.Unit(t, n, unit)
		if n.Role != fleet.RoleNameserver {
			if state == "active" {
				t.Errorf("%s is not a nameserver but runs %s", n.Name, unit)
			}
			continue
		}
		if state != "active" {
			t.Errorf("nameserver %s: %s is %s", n.Name, unit, state)
			continue
		}
		if user := strings.TrimSpace(f.MustExec(t, n, "systemctl show -p User --value "+unit).Stdout); user != "orama-coredns" {
			t.Errorf("%s: %s runs as %q, want orama-coredns", n.Name, unit, user)
		}
	}
}

// servicesRunning is the summary line `orama node status` ends with, "5 of 5
// running" (production/status/command.go).
var servicesRunning = regexp.MustCompile(`(?m)^(\d+) of (\d+) running$`)

// allServicesRunning reports whether that line says every listed service runs
// and lists at least one: a bare "running" substring is also in "0 of 5
// running".
func allServicesRunning(out string) bool {
	m := servicesRunning.FindStringSubmatch(out)
	return m != nil && m[1] == m[2] && m[2] != "0"
}

// TestInstall_nodeStatusAndDoctor: the local commands an operator runs on
// the node itself report a healthy install: `orama node status` lists the
// services running, `orama node doctor` passes every check
// (docs/CLI_REFERENCE.md "orama node status", "orama node doctor").
func TestInstall_nodeStatusAndDoctor(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		st := infra.OnNode(t, f, n, "node", "status")
		if st.Exit != 0 || strings.Contains(st.Stdout, "No Orama services") || !allServicesRunning(st.Stdout) {
			t.Errorf("%s: orama node status exit %d:\n%s%s", n.Name, st.Exit, st.Stdout, st.Stderr)
		}
		doc := infra.OnNode(t, f, n, "node", "doctor")
		if doc.Exit != 0 || !strings.Contains(doc.Stdout, " 0 failed") {
			t.Errorf("%s: orama node doctor exit %d:\n%s%s", n.Name, doc.Exit, f.Redact(doc.Stdout), f.Redact(doc.Stderr))
		}
	}
}

// TestInstall_nodeReportOnNode: `orama node report` on the node is one line
// of JSON naming this node's WireGuard address and a settled raft state;
// --pretty indents the same document (docs/CLI_REFERENCE.md "orama node report").
func TestInstall_nodeReportOnNode(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		out := infra.OnNode(t, f, n, "node", "report")
		if out.Exit != 0 {
			t.Fatalf("%s: orama node report exit %d: %s", n.Name, out.Exit, f.Redact(out.Stderr))
		}
		if lines := strings.Count(strings.TrimSpace(out.Stdout), "\n"); lines != 0 {
			t.Errorf("%s: the default report is %d lines, want one", n.Name, lines+1)
		}
		var r monitor.NodeReport
		if err := json.Unmarshal([]byte(out.Stdout), &r); err != nil {
			t.Fatalf("%s: node report is not JSON: %v", n.Name, err)
		}
		if n.WGIP != "" && r.WGIP != n.WGIP {
			t.Errorf("%s: report WG IP %q, want %q", n.Name, r.WGIP, n.WGIP)
		}
		if r.RQLite == nil || (r.RQLite.RaftState != monitor.RaftLeader && r.RQLite.RaftState != monitor.RaftFollower) {
			t.Errorf("%s: raft state in the report is %+v", n.Name, r.RQLite)
		}
		pretty := infra.OnNode(t, f, n, "node", "report", "--pretty")
		if pretty.Exit != 0 || strings.Count(pretty.Stdout, "\n") < 10 {
			t.Errorf("%s: --pretty did not indent the report (exit %d)", n.Name, pretty.Exit)
		}
		for _, secret := range []string{"PrivateKey", "BEGIN PRIVATE KEY", "rqlite_password"} {
			if strings.Contains(out.Stdout, secret) {
				t.Errorf("%s: the node report carries %q", n.Name, secret)
			}
		}
	}
}
