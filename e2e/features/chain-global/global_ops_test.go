//go:build e2e_fleet

package chainglobal

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Layout of the installed global services (core/pkg/constants/global.go and
// chain.go): where the installer puts binaries and units, and the units the
// fingerprint of an install lists.
const (
	globalBinDir   = "/usr/lib/orama-global"
	globalUnitGlob = "orama-global*"
	unitDir        = "/etc/systemd/system"
)

// asRoot runs `orama <args>` on n as root over SSH, the way an operator on
// the node does.
func asRoot(t *testing.T, f *fleet.Fleet, n fleet.Node, args ...string) fleet.Output {
	t.Helper()
	return infra.OnNode(t, f, n, args...)
}

// TestGlobalStatus_showsTheInstalledUnitsAndNeedsRoot: `orama global status`
// (run as root on the node) prints a SERVICE / UNIT / STATE row for every
// installed orama-global-* unit: the run's chain unit is installed and
// active on every node; an account with no privileges is refused with the
// usage code before anything is read.
func TestGlobalStatus_showsTheInstalledUnitsAndNeedsRoot(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for _, n := range c.Nodes() {
		out := asRoot(t, c.F, n, "global", "status")
		infra.ExpectNodeExit(t, n.Name+" global status", out, infra.ExitOK, "SERVICE", "UNIT", "STATE")
		found := false
		for _, line := range strings.Split(out.Stdout, "\n") {
			f := strings.Fields(line)
			if len(f) == 3 && f[0] == "chain" {
				found = true
				if f[1] != chain.Unit || f[2] != infra.UnitActive {
					t.Errorf("%s: the chain row is %v, want %s active", n.Name, f, chain.Unit)
				}
			}
		}
		if !found {
			t.Errorf("%s: global status lists no chain service:\n%s", n.Name, out.Stdout)
		}
	}
	infra.ExpectNodeExit(t, "global status as an unprivileged account", infra.OnNodeUnprivileged(t, c.F, c.Node(t, 0), "global", "status"), infra.ExitUsage, infra.MustBeRoot)
}

// TestValidatorGuard_checkSignFloorPassesWithNoFloorAndNeedsRoot: the
// double-sign guard passes on a validator whose key was never migrated (no
// sign floor is recorded and the key is in the chain home), prints nothing,
// and refuses an unprivileged account.
func TestValidatorGuard_checkSignFloorPassesWithNoFloorAndNeedsRoot(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	for _, n := range c.Nodes() {
		out := asRoot(t, c.F, n, "global", "validator", "check-sign-floor")
		infra.ExpectNodeExit(t, n.Name+" check-sign-floor", out, infra.ExitOK)
	}
	infra.ExpectNodeExit(t, "check-sign-floor as an unprivileged account",
		infra.OnNodeUnprivileged(t, c.F, c.Node(t, 0), "global", "validator", "check-sign-floor"), infra.ExitUsage, infra.MustBeRoot)
}

// installFingerprint is what a refused install must leave alone: the global
// binaries (name, size, mtime), the orama-global units, and the system
// accounts of the global services.
func installFingerprint(t *testing.T, f *fleet.Fleet, n fleet.Node) string {
	t.Helper()
	cmd := "find " + globalBinDir + " " + unitDir + " -maxdepth 3 -name '" + globalUnitGlob + "' -o -path '" + globalBinDir + "/bin/*' " +
		"| sort | xargs -r stat -c '%n %s %Y'; getent passwd | cut -d: -f1 | grep '^orama-' | sort"
	return f.MustExec(t, n, cmd).Stdout
}

// TestGlobalInstall_refusalsChangeNothing: every mistake `orama global
// install` can be told about is refused before the machine changes: a
// service list that is empty, unknown, without the chain, with a provider and
// no ipfs, or with a provider beside a repair delegate; --init-chain flags
// without --init-chain; --chain-client-user without --colocated or empty;
// and, as root, a missing storage budget, a persistent peer that is not
// id@host:port, an invalid chain id or ssh port, and a staged directory that
// holds no release. The node's binaries, units and accounts are exactly as
// they were, and an unprivileged account is refused the usage code. The
// install that succeeds needs a staged release and a machine that is not
// already running the chain under another layout: it is exercised by the
// stagenet deploy script, not here.
func TestGlobalInstall_refusalsChangeNothing(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, chain.OperatorNode)
	before := installFingerprint(t, c.F, n)
	const staged = "/e2e-no-such-staged-dir"
	usage := []struct {
		name string
		args []string
		want string
	}{
		{"no service", nil, "no global service named"},
		{"an unknown service", []string{"--services", "bogus"}, "unknown global service"},
		{"ipfs without the chain", []string{"--services", "ipfs"}, "add chain to --services"},
		{"a provider without ipfs", []string{"--services", "chain,provider"}, "add ipfs to --services"},
		{"a provider beside a repair delegate", []string{"--services", "chain,ipfs,provider,repair"}, "must not run beside"},
		{"init-chain flags without --init-chain", []string{"--services", "chain", "--chain-id", "e2e"}, "only apply with --init-chain"},
		{"a chain client user without --colocated", []string{"--services", "chain", "--chain-client-user", "bob"}, "only applies with --colocated"},
		{"an empty chain client user", []string{"--services", "chain", "--colocated", "--chain-client-user", " "}, "needs an account name"},
	}
	for _, tc := range usage {
		infra.ExpectNodeExit(t, tc.name, asRoot(t, c.F, n, append([]string{"global", "install"}, tc.args...)...), infra.ExitUsage, tc.want)
	}
	failure := []struct {
		name string
		args []string
		want string
	}{
		{"ipfs with no storage budget", []string{"--services", "chain,ipfs", "--staged-dir", staged}, "--public-storage-gb"},
		{"a persistent peer that is not id@host:port", []string{"--services", "chain", "--staged-dir", staged, "--persistent-peers", "nope"}, "persistent peer"},
		{"an invalid chain id", []string{"--services", "chain", "--staged-dir", staged, "--init-chain", "--chain-id", "BAD_ID", "--moniker", "m", "--genesis", "/g"}, "chain id"},
		{"an invalid ssh port", []string{"--services", "chain", "--staged-dir", staged, "--ssh-port", "70000"}, "is not a TCP port"},
		{"no genesis with --init-chain", []string{"--services", "chain", "--staged-dir", staged, "--init-chain", "--chain-id", "e2e", "--moniker", "m"}, "--genesis is required"},
		{"a staged directory with no release", []string{"--services", "chain", "--staged-dir", staged}, ""},
	}
	for _, tc := range failure {
		infra.ExpectNodeExit(t, tc.name, asRoot(t, c.F, n, append([]string{"global", "install"}, tc.args...)...), infra.ExitFailure, tc.want)
	}
	infra.ExpectNodeExit(t, "an install as an unprivileged account",
		infra.OnNodeUnprivileged(t, c.F, n, "global", "install", "--services", "chain", "--staged-dir", staged), infra.ExitUsage, infra.MustBeRoot)
	if after := installFingerprint(t, c.F, n); after != before {
		t.Fatalf("a refused install changed %s:\nbefore:\n%s\nafter:\n%s", n.Name, before, after)
	}
}

// optionalGlobalServices are the global services a node may run without:
// a fleet validator runs none of them, stagenet's nodes run all but repair.
var optionalGlobalServices = []string{"archiver", "repair", "indexer", "provider"}

// uninstalledGlobalService is an optional global service whose unit file the
// node does not have, which is what the lifecycle reads as "not installed".
func uninstalledGlobalService(t *testing.T, f *fleet.Fleet, n fleet.Node) string {
	t.Helper()
	for _, s := range optionalGlobalServices {
		if strings.TrimSpace(f.MustExec(t, n, "test -e /etc/systemd/system/orama-global-"+s+".service && echo installed || echo absent").Stdout) == "absent" {
			return s
		}
	}
	t.Fatalf("%s has every optional global service installed (%v): no service is left to show the not-installed refusal", n.Name, optionalGlobalServices)
	return ""
}

// TestGlobalLifecycle_usageErrors: start, stop and restart refuse a service
// name that is not one of chain, ipfs, provider, archiver, indexer or repair
// with the usage code, a service that is not installed on this node with a
// failure that says so (nothing is stopped or started), and an unprivileged
// account; the chain keeps running through all of it. The verbs themselves
// are exercised by chain-global-destructive.
func TestGlobalLifecycle_usageErrors(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 1)
	absent := uninstalledGlobalService(t, c.F, n)
	for _, verb := range []string{"start", "stop", "restart"} {
		infra.ExpectNodeExit(t, verb+" of an unknown service", asRoot(t, c.F, n, "global", verb, "e2e-bogus"), infra.ExitUsage, "unknown global service")
		infra.ExpectNodeExit(t, verb+" of a service that is not installed", asRoot(t, c.F, n, "global", verb, absent), infra.ExitFailure, absent+" is not installed")
		infra.ExpectNodeExit(t, verb+" as an unprivileged account", infra.OnNodeUnprivileged(t, c.F, n, "global", verb), infra.ExitUsage, infra.MustBeRoot)
	}
	infra.ExpectNodeExit(t, "status takes no argument", asRoot(t, c.F, n, "global", "status", "chain"), infra.ExitUsage)
	if state := c.F.Unit(t, n, chain.Unit); state != infra.UnitActive {
		t.Errorf("%s: the chain unit is %s after refused lifecycle commands", n.Name, state)
	}
}

// TestGlobalGroups_listTheirSubcommands: `orama global validator` and
// `orama global validator migrate` print their help with every subcommand
// (exit 0) and refuse an unknown subcommand with the usage code.
func TestGlobalGroups_listTheirSubcommands(t *testing.T) {
	t.Parallel()
	groups := map[string][]string{
		"validator":         {"check-sign-floor", "edit", "export-key", "migrate", "reseal", "unjail"},
		"validator migrate": {"cancel", "export", "import", "prepare"},
	}
	for group, subs := range groups {
		args := append([]string{"global"}, strings.Fields(group)...)
		res := infra.Run(t, harness.CLI(t), args...)
		infra.ExpectExit(t, res, infra.ExitOK, subs...)
		bad := infra.Run(t, harness.CLI(t), append(args, "e2e-no-such-subcommand")...)
		infra.ExpectExit(t, bad, infra.ExitUsage, "e2e-no-such-subcommand")
	}
}
