//go:build e2e_fleet

package bootstrap

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// What `orama maint cluster settings` prints (core/cmd/orama/internal/cmd/clustercmd/cluster.go).
const (
	settingMode = "namespace-creation"
	settingCap  = "max-namespaces-per-wallet"
	modeOpen    = "open"
	openedLine  = "Namespace creation is open."
)

var settingLine = regexp.MustCompile(`(?m)^(namespace-creation|max-namespaces-per-wallet):\s*(\S+)\s*$`)

// TestBootstrap_namespaceCreationOpenForTheRun sets namespace creation to
// open and does not restore it: every later stage creates namespaces as
// fresh wallets (e2e/README.md "Bootstrap contract", docs/CLI_REFERENCE.md
// "orama maint cluster settings set"). It is not parallel, so it runs before this
// package's parallel tests, and a fresh wallet then creates a namespace.
func TestBootstrap_namespaceCreationOpenForTheRun(t *testing.T) {
	cli := harness.CLI(t)
	res := cli.MustOK(t, "maint", "cluster", "settings", "set", settingMode, modeOpen)
	if !strings.Contains(res.Stdout, openedLine) {
		t.Errorf("set printed %q, want %q", res.Stdout, openedLine)
	}
	again := cli.MustOK(t, "maint", "cluster", "settings", "set", settingMode, modeOpen)
	if !strings.Contains(again.Stdout, openedLine) {
		t.Errorf("setting open twice is not idempotent: %q", again.Stdout)
	}
	show := cli.MustOK(t, "maint", "cluster", "settings", "show").Stdout
	got := map[string]string{}
	for _, m := range settingLine.FindAllStringSubmatch(show, -1) {
		got[m[1]] = m[2]
	}
	if got[settingMode] != modeOpen {
		t.Fatalf("settings show says namespace-creation %q, want %q:\n%s", got[settingMode], modeOpen, show)
	}
	if n, err := strconv.Atoi(got["max-namespaces-per-wallet"]); err != nil || n < 1 {
		t.Errorf("per-wallet cap %q is not a positive number:\n%s", got["max-namespaces-per-wallet"], show)
	}
	ns.New(t, harness.Fleet(t), ns.Options{Via: ns.ViaUser})
}

// TestBootstrap_operatorCapCoversTheRun raises the per-wallet namespace cap
// when the run could need more than it allows, and does not restore it. Every
// ViaOperator namespace is owned by the run's operator wallet, alongside what
// it already owns; with the fleet's live-namespace cap above the default
// per-wallet cap of ten, parallel packages were refused NAMESPACE_QUOTA.
// It is not parallel, so it runs before this package's parallel tests.
func TestBootstrap_operatorCapCoversTheRun(t *testing.T) {
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	live, err := ns.MaxLiveForTarget(os.LookupEnv, f.State.IsStagenet(), len(f.State.Nodes))
	if err != nil {
		t.Fatal(err)
	}
	owned := 0
	for _, line := range strings.Split(cli.MustOK(t, "namespace", "list").Stdout, "\n")[1:] {
		if strings.TrimSpace(line) != "" {
			owned++
		}
	}
	need := live + owned
	show := cli.MustOK(t, "maint", "cluster", "settings", "show").Stdout
	walletCap := 0
	for _, m := range settingLine.FindAllStringSubmatch(show, -1) {
		if m[1] == settingCap {
			walletCap, _ = strconv.Atoi(m[2])
		}
	}
	if walletCap >= need {
		return
	}
	cli.MustOK(t, "maint", "cluster", "settings", "set", settingCap, strconv.Itoa(need))
	after := cli.MustOK(t, "maint", "cluster", "settings", "show").Stdout
	if !strings.Contains(after, settingCap+": "+strconv.Itoa(need)) {
		t.Fatalf("the per-wallet cap was not raised to %d:\n%s", need, after)
	}
}

// TestBootstrap_invalidModeRefusedKeepsOpen: a mode that does not exist is a
// refusal that changes nothing, so a typo cannot close the run's creation.
func TestBootstrap_invalidModeRefusedKeepsOpen(t *testing.T) {
	cli := harness.CLI(t)
	for _, bad := range []string{"", "OPEN", "open; DROP TABLE settings", "\u202enepo", strings.Repeat("o", 4096)} {
		res := infra.Run(t, cli, "maint", "cluster", "settings", "set", settingMode, bad)
		if res.Exit == infra.ExitOK {
			t.Errorf("mode %.40q was accepted: %s", bad, res.Stdout)
		}
	}
	show := cli.MustOK(t, "maint", "cluster", "settings", "show").Stdout
	if !strings.Contains(show, settingMode+": "+modeOpen) {
		t.Fatalf("a refused set changed the mode:\n%s", show)
	}
}

// TestBootstrap_operatorIsTheTestWallet: the genesis operator is the run's
// throwaway wallet (the agent signed the install), so every operator command
// of the run acts as it (docs/AUTH.md "Operating the cluster").
func TestBootstrap_operatorIsTheTestWallet(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	out := harness.CLI(t).MustOK(t, "maint", "operator", "list").Stdout
	want := strings.ToLower(f.State.OperatorAddress)
	if want == "" {
		t.Fatal("the run recorded no operator address")
	}
	if !strings.Contains(strings.ToLower(out), want) {
		t.Fatalf("operator list does not name the test wallet %s:\n%s", f.State.OperatorAddress, out)
	}
}

// TestBootstrap_environmentIsActive: the CLI's active environment is the
// run's, pointing at the run's gateway (docs/CLI_REFERENCE.md "orama network").
func TestBootstrap_environmentIsActive(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := harness.CLI(t)
	cur := cli.MustOK(t, "network", "current").Stdout
	if !strings.Contains(cur, "Current network: "+f.State.Env) || !strings.Contains(cur, f.State.GatewayURL) {
		t.Errorf("env current does not name %s at %s:\n%s", f.State.Env, f.State.GatewayURL, cur)
	}
	list := cli.MustOK(t, "network", "list").Stdout
	if !strings.Contains(list, f.State.Env) {
		t.Errorf("env list lacks %s:\n%s", f.State.Env, list)
	}
	for _, forbidden := range []string{"testnet", "mainnet"} {
		if strings.Contains(cur, forbidden) {
			t.Fatalf("the active environment mentions %s:\n%s", forbidden, cur)
		}
	}
}
