package systemd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// unitDir locates core/systemd by walking up to the module root, so it cannot
// accidentally match this package's own directory.
func unitDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "systemd")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test working directory")
		}
		dir = parent
	}
}

func readUnit(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(unitDir(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

// PartOf propagates stop AND restart. With it, `orama node restart` tore the
// overlay down and brought it back, severing every namespace raft and Olric
// memberlist on the node. The mesh is infrastructure: it outlives the
// supervisor that manages the services riding on it.
func TestWireGuardUnitIsNotPartOfTheSupervisor(t *testing.T) {
	unit := readUnit(t, "orama-namespace-wireguard@.service")

	for _, line := range strings.Split(unit, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "PartOf=") {
			t.Errorf("WireGuard unit declares %q; a supervisor restart must not bounce wg0", trimmed)
		}
		// An ExecStop that runs wg-quick down would defeat the same guarantee
		// from the other direction.
		if strings.HasPrefix(trimmed, "ExecStop=") && strings.Contains(trimmed, "wg-quick down") {
			t.Errorf("WireGuard unit declares %q; stopping the unit must leave the interface up", trimmed)
		}
	}
}

// Olric, the SFU and TURN bind the WireGuard address directly, and rqlite and
// the gateway talk to peers across it. On a cold boot they can otherwise start
// before wg0 exists and fail-loop until something restarts them.
func TestWGBindingUnitsAreOrderedAfterTheMesh(t *testing.T) {
	const dep = "orama-namespace-wireguard@index.service"

	units := []string{
		"orama-namespace-rqlite@.service",
		"orama-namespace-olric@.service",
		"orama-namespace-gateway@.service",
		"orama-namespace-pubsub@.service",
		"orama-namespace-sfu@.service",
		"orama-namespace-turn@.service",
		"orama-namespace-ipfs@.service",
		"orama-namespace-vault@.service",
	}

	for _, name := range units {
		t.Run(name, func(t *testing.T) {
			unit := readUnit(t, name)
			var after string
			for _, line := range strings.Split(unit, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "After=") {
					after += line
				}
			}
			if after == "" {
				t.Fatalf("%s declares no After=", name)
			}
			// ipfs@ and vault@ order on the templated instance rather than the
			// index one; either expresses the same dependency.
			if !strings.Contains(after, dep) && !strings.Contains(after, "orama-namespace-wireguard@%i.service") {
				t.Errorf("%s is not ordered after the WireGuard mesh:\n  %s", name, after)
			}
		})
	}
}

// The templates must all be installed, or a unit ordered after the mesh refers
// to something systemd has never seen.
func TestWireGuardTemplateIsInstalled(t *testing.T) {
	found := false
	for _, u := range UnitFilesToInstall() {
		if u == "orama-namespace-wireguard@.service" {
			found = true
		}
	}
	if !found {
		t.Error("the WireGuard template is not in the installed unit set")
	}
}

// unitDirectives returns every `Key=` line in a unit, skipping comments and
// section headers. Repeated keys are preserved in order.
//
// It does not join backslash line-continuations: a continuation line that
// happened to contain an `=` would be read as a directive of its own. No unit
// in this repository has one, and the assertions below only read [Unit]-section
// keys, which are single-line by convention.
func unitDirectives(unit string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(unit, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		out[key] = append(out[key], value)
	}
	return out
}

// Requires propagates stop AND restart. The gateway declaring it on rqlite and
// Olric meant a `systemctl restart orama-namespace-rqlite@<ns>` — which the
// split-brain recovery path issues on its own — bounced the gateway with it,
// restarting the leader wait, the health monitor, the cluster manager, every
// reconciler and the tenant restore, for a database restart the gateway is
// built to ride out.
func TestGatewayDoesNotHardRequireItsBackends(t *testing.T) {
	d := unitDirectives(readUnit(t, "orama-namespace-gateway@.service"))

	for _, requires := range d["Requires"] {
		t.Errorf("gateway declares Requires=%s; a backend restart must not bounce the gateway", requires)
	}

	// Ordering and pull-in must survive the downgrade, or a cold boot starts
	// the gateway against nothing.
	joined := strings.Join(slices.Concat(d["After"], d["Wants"]), " ")
	for _, backend := range []string{"orama-namespace-rqlite@%i.service", "orama-namespace-olric@%i.service"} {
		if !strings.Contains(joined, backend) {
			t.Errorf("gateway no longer orders on %s", backend)
		}
	}
}

// Olric is an in-memory cache with its own memberlist. It never reads the
// database, so coupling it to rqlite only meant a rqlite restart threw away
// every cached entry on the node.
func TestOlricIsNotCoupledToRQLite(t *testing.T) {
	d := unitDirectives(readUnit(t, "orama-namespace-olric@.service"))

	for _, key := range []string{"Requires", "After", "Wants", "BindsTo", "PartOf"} {
		for _, value := range d[key] {
			if strings.Contains(value, "orama-namespace-rqlite") {
				t.Errorf("olric declares %s=%s; it has no functional dependency on rqlite", key, value)
			}
		}
	}
}

// ipfs-cluster and the GC job genuinely cannot do anything without their
// node's IPFS daemon, so Requires is right there and must not be "cleaned up"
// by analogy with the gateway.
func TestIPFSControllersKeepTheirHardDependency(t *testing.T) {
	for _, name := range []string{"orama-namespace-ipfs-cluster@.service", "orama-namespace-ipfs-gc@.service"} {
		t.Run(name, func(t *testing.T) {
			d := unitDirectives(readUnit(t, name))
			if !strings.Contains(strings.Join(d["Requires"], " "), "orama-namespace-ipfs@%i.service") {
				t.Errorf("%s no longer requires its IPFS daemon", name)
			}
		})
	}
}

// oneshotUnits are the installed units that run to completion rather than
// staying up, so the restart policy below does not apply to them. Listing them
// explicitly is what lets supervisedUnits be derived: a new template is either
// one of these or it is supervised, and the test makes you say which.
var oneshotUnits = []string{
	"orama-namespace-wireguard@.service", // adopts wg0 in place, RemainAfterExit
	"orama-namespace-ipfs-gc@.service",   // `ipfs repo gc`, fired by its timer
	"orama-namespace-ipfs-gc@.timer",
}

// supervisedUnits are the long-running units orama-node starts and reconciles:
// everything installed that is not a oneshot. Derived rather than listed, so a
// template added tomorrow cannot silently skip the restart policy.
func supervisedUnits() []string {
	var out []string
	for _, unit := range UnitFilesToInstall() {
		if !slices.Contains(oneshotUnits, unit) {
			out = append(out, unit)
		}
	}
	return out
}

// Every installed unit must be classified. A new template that is neither
// listed as a oneshot nor covered by the policy assertions would otherwise pass
// both of the tests below by simply not being in either set.
func TestEveryInstalledUnitIsClassified(t *testing.T) {
	installed := UnitFilesToInstall()

	for _, unit := range oneshotUnits {
		if !slices.Contains(installed, unit) {
			t.Errorf("%q is listed as a oneshot but is not installed; drop it from oneshotUnits", unit)
		}
	}

	if got := len(supervisedUnits()) + len(oneshotUnits); got != len(installed) {
		t.Errorf("classified %d units, %d are installed", got, len(installed))
	}
}

// A unit that hits systemd's default start limit refuses `systemctl start`
// with "Start request repeated too quickly" until someone runs reset-failed.
// orama-node's boot supervisor reconciles these units, so that turns a
// transient failure into one only a human can clear. Before this was explicit,
// "retry forever" happened by accident: RestartSec=5 never trips the default
// 5-starts-in-10s window.
func TestSupervisedUnitsHaveAnExplicitRestartPolicy(t *testing.T) {
	for _, name := range supervisedUnits() {
		t.Run(name, func(t *testing.T) {
			d := unitDirectives(readUnit(t, name))

			if got := d["StartLimitIntervalSec"]; len(got) != 1 || got[0] != "0" {
				t.Errorf("%s: StartLimitIntervalSec = %v, want [0]", name, got)
			}
			if got := d["Restart"]; len(got) != 1 || got[0] != "always" {
				t.Errorf("%s: Restart = %v, want [always] — on-failure leaves a daemon down after a clean exit", name, got)
			}
			if got := d["RestartSec"]; len(got) != 1 || got[0] != "5s" {
				t.Errorf("%s: RestartSec = %v, want [5s]", name, got)
			}
		})
	}
}

// StartLimitIntervalSec moved from [Service] to [Unit] in systemd 229. In the
// wrong section it is accepted for compatibility but warned about, and it is
// easy to get wrong because the neighbouring Restart= directives are in
// [Service].
func TestStartLimitIsDeclaredInTheUnitSection(t *testing.T) {
	for _, name := range supervisedUnits() {
		t.Run(name, func(t *testing.T) {
			unit := readUnit(t, name)
			serviceAt := strings.Index(unit, "\n[Service]\n")
			if serviceAt < 0 {
				t.Fatalf("%s has no [Service] section", name)
			}
			if !strings.Contains(unit[:serviceAt], "StartLimitIntervalSec=0") {
				t.Errorf("%s declares StartLimitIntervalSec outside [Unit]", name)
			}
		})
	}
}
