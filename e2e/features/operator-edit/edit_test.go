//go:build e2e_fleet

package operatoredit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Paths and units of the global layer on a node (core/pkg/constants/global.go).
const (
	unitGlob       = "/etc/systemd/system/orama-global-*.service"
	ipfsUnit       = "/etc/systemd/system/orama-global-ipfs.service"
	relayUnit      = "/etc/systemd/system/orama-global-tor-relay.service"
	ipfsConfig     = "/var/lib/orama-global/ipfs/config"
	relayTorrc     = "/var/lib/orama-global/tor-relay.torrc"
	torNetworkFile = "/var/lib/orama-global/tor-network.json"
	backupSuffix   = ".e2e-edit"
)

// Refusals (core/cmd/orama/internal/production/nodeedit/plan.go).
const (
	noStorage = "no public storage"
	noRelay   = "no Tor relay"
	setupFlow = "setup flow"
	removeCmd = "orama remove --node "
)

// has reports whether path exists on n.
func has(t testing.TB, f *fleet.Fleet, n fleet.Node, test string) bool {
	t.Helper()
	return f.Exec(t, n, test).Exit == 0
}

func hasGlobalLayer(t testing.TB, f *fleet.Fleet, n fleet.Node) bool {
	t.Helper()
	return has(t, f, n, "ls "+unitGlob+" >/dev/null 2>&1")
}

// TestEdit_nothingNamedIsAUsageError: without a setting, outside a terminal,
// there is nothing to do and no form to ask.
func TestEdit_nothingNamedIsAUsageError(t *testing.T) {
	f := harness.Fleet(t)

	res := infra.Run(t, harness.CLI(t), "edit", "--env", f.State.Env, "--node", f.State.Nodes[0].PublicIP)

	infra.ExpectExit(t, res, infra.ExitUsage, "name what to change")
}

// TestEdit_anUnknownNodeIsNotFound: --node names a node of the environment.
func TestEdit_anUnknownNodeIsNotFound(t *testing.T) {
	f := harness.Fleet(t)

	res := infra.Run(t, harness.CLI(t), "edit", "--env", f.State.Env, "--node", "192.0.2.1", "--exit=true", "--yes")

	infra.ExpectRefused(t, res, "192.0.2.1", "not in this network")
}

// TestEdit_aNodeWithoutTheGlobalLayerRefusesEveryChange: with no public Kubo and
// no relay there is nothing to resize or switch, turning the layer on is the
// setup flow's job, and the layer already off is not a change. Nothing on the
// node changes.
func TestEdit_aNodeWithoutTheGlobalLayerRefusesEveryChange(t *testing.T) {
	f := harness.Fleet(t)
	var plain []fleet.Node
	for _, n := range f.State.Nodes {
		if !hasGlobalLayer(t, f, n) {
			plain = append(plain, n)
		}
	}
	if len(plain) == 0 {
		harness.SkipNotApplicable(t, "every node of the run has the global layer; provision a cluster-only node to test the refusals")
	}
	n, cli := plain[0], harness.CLI(t)

	infra.ExpectExit(t, infra.Run(t, cli, "edit", "--env", f.State.Env, "--node", n.PublicIP, "--storage-gb", "10", "--no-chain", "--yes"),
		infra.ExitConflict, noStorage, "Nothing was changed")
	infra.ExpectExit(t, infra.Run(t, cli, "edit", "--env", f.State.Env, "--node", n.PublicIP, "--exit=true", "--yes"),
		infra.ExitConflict, noRelay)
	infra.ExpectExit(t, infra.Run(t, cli, "edit", "--env", f.State.Env, "--node", n.PublicIP, "--global=true", "--yes"),
		infra.ExitConflict, setupFlow)
	infra.ExpectExit(t, infra.Run(t, cli, "edit", "--env", f.State.Env, "--node", n.PublicIP, "--global=false", "--yes"),
		infra.ExitOK, "already off")
}

// TestEdit_aNodeWithTheGlobalLayerIsNotStrippedByEdit: turning the layer off is
// refused, because the node holds the validator's key and the node's bonds, and
// the refusal names the command that takes a node out.
func TestEdit_aNodeWithTheGlobalLayerIsNotStrippedByEdit(t *testing.T) {
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		if !hasGlobalLayer(t, f, n) {
			continue
		}
		res := infra.Run(t, harness.CLI(t), "edit", "--env", f.State.Env, "--node", n.PublicIP, "--global=false", "--yes")

		infra.ExpectExit(t, res, infra.ExitConflict, "consensus key", removeCmd+n.PublicIP)
		if !hasGlobalLayer(t, f, n) {
			t.Fatalf("a refused edit removed the global layer of %s", n.Name)
		}
		return
	}
	harness.SkipNotApplicable(t, "no node of the run has the global layer; run against a chain-enabled fleet or the stagenet target")
}

// backup copies path to path+backupSuffix on n and restores it when the test
// ends.
func backup(t testing.TB, f *fleet.Fleet, n fleet.Node, path, restart string) {
	t.Helper()
	f.MustExec(t, n, "cp -p "+fleet.ShellQuote(path)+" "+fleet.ShellQuote(path+backupSuffix))
	t.Cleanup(func() {
		f.MustExec(t, n, "mv -f "+fleet.ShellQuote(path+backupSuffix)+" "+fleet.ShellQuote(path))
		if out := infra.OnNode(t, f, n, "global", "restart", restart); out.Exit != 0 {
			t.Errorf("restarting %s on %s after restoring %s: exit %d\n%s", restart, n.Name, path, out.Exit, f.Redact(out.Stdout+out.Stderr))
		}
	})
}

// TestEdit_storageSizesThePublicKubo: on a node with the public Kubo, edit
// --storage-gb --no-chain sets its StorageMax to the capacity plus ten percent
// and keeps the rest of its config; the node's own config is put back afterwards.
func TestEdit_storageSizesThePublicKubo(t *testing.T) {
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		if !has(t, f, n, "test -e "+ipfsUnit) {
			continue
		}
		backup(t, f, n, ipfsConfig, "ipfs")

		res := infra.RunFor(t, harness.CLI(t), infra.UpgradeBudget, "edit", "--env", f.State.Env, "--node", n.PublicIP, "--storage-gb", "20", "--no-chain", "--yes")

		infra.ExpectExit(t, res, infra.ExitOK, "edited")
		var cfg struct {
			Datastore struct{ StorageMax string }
			Identity  struct{ PeerID string }
		}
		if err := json.Unmarshal(f.ReadFile(t, n, ipfsConfig), &cfg); err != nil {
			t.Fatalf("the Kubo config is not JSON after the edit: %v", err)
		}
		if cfg.Datastore.StorageMax != "22GB" {
			t.Errorf("StorageMax = %q, want 22GB (20 GB declared plus ten percent)", cfg.Datastore.StorageMax)
		}
		if cfg.Identity.PeerID == "" {
			t.Error("the edit lost the repo's identity")
		}
		return
	}
	harness.SkipNotApplicable(t, "no node of the run has the public Kubo (orama-global-ipfs); run against a chain-enabled fleet or the stagenet target")
}

// TestEdit_exitSwitchesOnlyTheRelaysExitSection: on a node with a Tor relay, edit
// --exit=true on a network that allows exits rewrites the exit section and
// nothing before it; a network that forbids exits is refused.
func TestEdit_exitSwitchesOnlyTheRelaysExitSection(t *testing.T) {
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		if !has(t, f, n, "test -e "+relayUnit) {
			continue
		}
		var network struct {
			AllowExit bool `json:"allow_exit"`
		}
		if err := json.Unmarshal(f.ReadFile(t, n, torNetworkFile), &network); err != nil {
			t.Fatalf("%s is not JSON: %v", torNetworkFile, err)
		}
		if !network.AllowExit {
			res := infra.Run(t, harness.CLI(t), "edit", "--env", f.State.Env, "--node", n.PublicIP, "--exit=true", "--yes")
			infra.ExpectExit(t, res, infra.ExitFailure, "does not allow exits")
			return
		}
		before := string(f.ReadFile(t, n, relayTorrc))
		backup(t, f, n, relayTorrc, "relay")

		res := infra.RunFor(t, harness.CLI(t), infra.UpgradeBudget, "edit", "--env", f.State.Env, "--node", n.PublicIP, "--exit=true", "--yes")

		infra.ExpectExit(t, res, infra.ExitOK, "edited")
		after := string(f.ReadFile(t, n, relayTorrc))
		if !strings.Contains(after, "ExitRelay 1") {
			t.Errorf("the relay is not an exit after the edit:\n%s", after)
		}
		if head := before[:strings.Index(before, "ExitRelay")]; !strings.HasPrefix(after, head) {
			t.Errorf("the edit changed more than the exit section:\n--- before\n%s\n--- after\n%s", before, after)
		}
		return
	}
	harness.SkipNotApplicable(t, "no node of the run has a Tor relay (orama-global-tor-relay); run against the stagenet target")
}

// TestRefresh_aSecondRefreshReplacesNothingAndRestartsNothing: the node's own
// refresh brings the global binaries to the staged release, so running it again
// replaces no binary and restarts no service; a node without the global layer
// refuses it.
func TestRefresh_aSecondRefreshReplacesNothingAndRestartsNothing(t *testing.T) {
	f := harness.Fleet(t)
	for _, n := range f.State.Nodes {
		out := infra.OnNode(t, f, n, "maint", "global", "refresh")
		if !hasGlobalLayer(t, f, n) {
			if out.Exit != infra.ExitFailure || !strings.Contains(out.Stdout+out.Stderr, "no orama-global unit is installed") {
				t.Errorf("%s has no global layer: refresh exit %d, want %d naming it:\n%s", n.Name, out.Exit, infra.ExitFailure, f.Redact(out.Stdout+out.Stderr))
			}
			continue
		}
		if out.Exit != infra.ExitOK {
			t.Errorf("%s: the first refresh exit %d:\n%s", n.Name, out.Exit, f.Redact(out.Stdout+out.Stderr))
			continue
		}
		again := infra.OnNode(t, f, n, "maint", "global", "refresh")
		if again.Exit != infra.ExitOK || !strings.Contains(again.Stdout, "no running service needs a restart") || strings.Contains(again.Stdout, "replaced") {
			t.Errorf("%s: a second refresh exit %d, want a no-op:\n%s", n.Name, again.Exit, f.Redact(again.Stdout+again.Stderr))
		}
	}
}

// TestGlobalEdit_refusesWhatTheNodeCannotDo: the node's own edit needs a setting,
// and on a node without a public Kubo or a relay it says so and changes nothing.
func TestGlobalEdit_refusesWhatTheNodeCannotDo(t *testing.T) {
	f := harness.Fleet(t)
	n := f.State.Nodes[0]

	none := infra.OnNode(t, f, n, "maint", "global", "edit")
	if none.Exit != infra.ExitUsage || !strings.Contains(none.Stdout+none.Stderr, "name a setting to change") {
		t.Errorf("edit with no setting: exit %d, want %d:\n%s", none.Exit, infra.ExitUsage, f.Redact(none.Stdout+none.Stderr))
	}
	if !has(t, f, n, "test -e "+ipfsUnit) {
		out := infra.OnNode(t, f, n, "maint", "global", "edit", "--storage-gb", "5")
		if out.Exit != infra.ExitFailure || !strings.Contains(out.Stdout+out.Stderr, "public Kubo is not installed") {
			t.Errorf("a resize without a Kubo: exit %d:\n%s", out.Exit, f.Redact(out.Stdout+out.Stderr))
		}
	}
	if !has(t, f, n, "test -e "+relayUnit) {
		out := infra.OnNode(t, f, n, "maint", "global", "edit", "--exit=true")
		if out.Exit != infra.ExitFailure || !strings.Contains(out.Stdout+out.Stderr, "no Tor relay") {
			t.Errorf("an exit switch without a relay: exit %d:\n%s", out.Exit, f.Redact(out.Stdout+out.Stderr))
		}
	}
}
