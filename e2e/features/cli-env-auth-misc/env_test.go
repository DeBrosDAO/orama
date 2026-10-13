//go:build e2e_fleet

package clienvauthmisc

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// concurrentAdds is how many `orama network add` run at once in one HOME.
const concurrentAdds = 8

// TestEnvAdd_customEnvironmentSignsInThroughIt: an environment added with the
// run's CA and made active is what the next command talks to, end to end: a
// real wallet login through it and whoami answered by its gateway
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-network-add, #orama-network-use).
func TestEnvAdd_customEnvironmentSignsInThroughIt(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	name := e2eEnvPrefix + "alt"
	added := cli.MustOK(t, "network", "add", name, f.State.GatewayURL, "e2e alternate", "--ca-file", f.State.CAFile)
	for _, want := range []string{"Added network: " + name, f.State.GatewayURL, "Trusted CA:"} {
		if !strings.Contains(added.Stdout, want) {
			t.Errorf("env add output lacks %q:\n%s", want, added.Stdout)
		}
	}
	cli.MustOK(t, "network", "use", name)
	if cur := cli.MustOK(t, "network", "current").Stdout; !strings.Contains(cur, "Current network: "+name) {
		t.Fatalf("env current after env use %s:\n%s", name, cur)
	}
	cli.MustOK(t, "auth", "login")
	t.Cleanup(func() {
		// t.Context() is cancelled before cleanups run: MustOK would fail
		// at once and leave the session signed in.
		ctx, cancel := fleet.CleanupContext(t)
		defer cancel()
		if res, err := cli.Run(ctx, "auth", "logout"); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: orama auth logout through %s: exit %d: %v\n%s", name, res.Exit, err, res.Stderr)
		}
	})
	who := cli.MustOK(t, "auth", "whoami").Stdout
	if !strings.Contains(strings.ToLower(who), strings.ToLower(f.State.OperatorAddress)) {
		t.Errorf("whoami through %s does not name the operator %s:\n%s", name, f.State.OperatorAddress, who)
	}
}

// TestEnvAdd_missingOrSurplusArgumentsAreUsage: add takes a name, a gateway
// URL and an optional description (cobra.RangeArgs(2, 3)).
func TestEnvAdd_missingOrSurplusArgumentsAreUsage(t *testing.T) {
	t.Parallel()
	cli := isolated(t)
	before := readEnvFile(t, cli)
	for _, args := range [][]string{
		{"network", "add"},
		{"network", "add", e2eEnvPrefix + "one"},
		{"network", "add", e2eEnvPrefix + "four", "https://a.invalid", "desc", "surplus"},
		{"network", "use"},
		{"network", "use", "a", "b"},
		{"network", "remove"},
		{"network", "remove", "a", "b"},
	} {
		if res := run(t, cli, args...); res.Exit != exitUsage {
			t.Errorf("orama %q: exit %d, want %d\n%s", args, res.Exit, exitUsage, output(res))
		}
	}
	if after := readEnvFile(t, cli); len(after.Environments) != len(before.Environments) || after.ActiveEnvironment != before.ActiveEnvironment {
		t.Errorf("refused command lines changed the environment list: %+v -> %+v", before, after)
	}
}

// TestEnvAdd_emptyNameOrUnusableURLRefused: an environment with no name, or
// a gateway URL no command can reach, is a bad value on the command line,
// which clierr classifies as usage (exit 2), and must not be stored.
func TestEnvAdd_emptyNameOrUnusableURLRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	for label, args := range map[string][]string{
		"empty name":         {"", f.State.GatewayURL},
		"blank name":         {"   ", f.State.GatewayURL},
		"empty url":          {e2eEnvPrefix + "nourl", ""},
		"url without host":   {e2eEnvPrefix + "nohost", "https://"},
		"not a url":          {e2eEnvPrefix + "noturl", "not a url"},
		"unsupported scheme": {e2eEnvPrefix + "ftp", "ftp://" + f.State.BaseDomain},
	} {
		t.Run(strings.ReplaceAll(label, " ", "_"), func(t *testing.T) {
			t.Parallel()
			cli := isolated(t)
			before := len(readEnvFile(t, cli).Environments)
			res := run(t, cli, append([]string{"network", "add"}, args...)...)
			if res.Exit != exitUsage {
				t.Errorf("env add with %s: exit %d, want %d\n%s", label, res.Exit, exitUsage, output(res))
			}
			if after := len(readEnvFile(t, cli).Environments); after != before {
				t.Errorf("env add with %s stored it (%d -> %d environments)", label, before, after)
			}
		})
	}
}

// TestEnvAdd_updatesInPlace: adding a name that exists updates its gateway
// and description (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-network-add "or update one already
// configured"); the CA stays for the same host and is dropped when the
// environment is pointed at another host (environment.go AddEnvironment).
func TestEnvAdd_updatesInPlace(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	name := e2eEnvPrefix + "upd"
	cli.MustOK(t, "network", "add", name, f.State.GatewayURL, "one", "--ca-file", f.State.CAFile)
	cli.MustOK(t, "network", "add", name, f.State.GatewayURL, "two")
	if ca, _ := caFileOf(t, cli, name); ca == "" {
		t.Error("re-adding the same host dropped its CA")
	}
	if got := networkRow(t, cli, name)["description"]; got != "two" {
		t.Errorf("the description of %s after the update is %q, want two", name, got)
	}
	cli.MustOK(t, "network", "add", name, "https://other."+f.State.BaseDomain, "three")
	if ca, ok := caFileOf(t, cli, name); !ok || ca != "" {
		t.Errorf("pointing %s at another host kept its CA %q (present %v)", name, ca, ok)
	}
	count := 0
	for _, e := range readEnvFile(t, cli).Environments {
		if e.Name == name {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%s is stored %d times, want once", name, count)
	}
}

// TestEnvUse_unknownIsNotFound: switching to an environment that is not
// configured is "not found" (exit 4) and leaves the active one alone.
func TestEnvUse_unknownIsNotFound(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	for _, name := range []string{e2eEnvPrefix + "absent", "production", "E2E-" + f.State.Env, "\u202e" + f.State.Env} {
		res := run(t, cli, "network", "use", name)
		if res.Exit != exitNotFound || !strings.Contains(output(res), "not found") {
			t.Errorf("env use %q: exit %d, want %d and \"not found\"\n%s", name, res.Exit, exitNotFound, output(res))
		}
	}
	if active := readEnvFile(t, cli).ActiveEnvironment; active != f.State.Env {
		t.Errorf("active environment is %q after refused switches, want %s", active, f.State.Env)
	}
}

// TestNetworkUse_hasNoAliases: `network use` answers to its own name only;
// `switch` and `enable`, which `orama env use` had, are unknown subcommands
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md#orama-network-use).
func TestNetworkUse_hasNoAliases(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	res := cli.MustOK(t, "network", "use", f.State.Env)
	if !strings.Contains(res.Stdout, f.State.Env) || !strings.Contains(res.Stdout, f.State.GatewayURL) {
		t.Errorf("network use %s printed:\n%s", f.State.Env, res.Stdout)
	}
	for _, alias := range []string{"switch", "enable"} {
		if res := run(t, cli, "network", alias, f.State.Env); res.Exit != exitUsage {
			t.Errorf("network %s: exit %d, want %d\n%s", alias, res.Exit, exitUsage, output(res))
		}
	}
}

// TestEnvRemove_absentSucceeds: removing an environment that is not there is
// nothing to do, not an error (environment.go RemoveEnvironment).
func TestEnvRemove_absentSucceeds(t *testing.T) {
	t.Parallel()
	cli := isolated(t)
	res := cli.MustOK(t, "network", "remove", e2eEnvPrefix+"never-added")
	if !strings.Contains(res.Stdout, "Removed network") {
		t.Errorf("env remove of an absent environment printed:\n%s", res.Stdout)
	}
}

// TestEnvRemove_activeLeavesNoneSelected: removing the active environment
// selects nothing else; the next gateway command asks for `orama network add` /
// `orama network use` instead of silently using another cluster (environment.go
// GetActiveEnvironment "No fallback").
func TestEnvRemove_activeLeavesNoneSelected(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	cli.MustOK(t, "network", "remove", f.State.Env)
	cur := run(t, cli, "network", "current")
	if cur.Exit != exitNotFound || !strings.Contains(output(cur), noEnvHelp) {
		t.Errorf("env current with nothing active: exit %d\n%s", cur.Exit, output(cur))
	}
	who := run(t, cli, "auth", "whoami")
	if who.Exit != exitUsage || !strings.Contains(output(who), noEnvHelp) {
		t.Errorf("auth whoami with no environment: exit %d, want %d naming %q\n%s", who.Exit, exitUsage, noEnvHelp, output(who))
	}
}

// TestNetworkList_emptyHomeConfiguresNothing: a fresh machine points at no
// cluster (environment.go LoadEnvironmentConfig "not filled in with somebody
// else's networks"): the list holds the networks built into the binary and not
// one configured gateway, and --json is the same rows as an array.
func TestNetworkList_emptyHomeConfiguresNothing(t *testing.T) {
	t.Parallel()
	cli := emptyHome(t)
	text := cli.MustOK(t, "network", "list").Stdout
	if strings.Contains(text, "configured") || strings.Contains(text, "*") {
		t.Errorf("orama network list on a fresh machine shows a configured or active network:\n%s", text)
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(cli.MustOK(t, "network", "list", "--json").Stdout), &rows); err != nil {
		t.Fatalf("orama network list --json is not a JSON array of rows: %v", err)
	}
	for _, row := range rows {
		if row["gateway"] != "-" || row["active"] != "" {
			t.Errorf("a fresh machine lists %v with a gateway or active", row)
		}
	}
	cur := run(t, cli, "network", "current")
	if cur.Exit != exitNotFound || !strings.Contains(output(cur), noEnvHelp) {
		t.Errorf("env current on a fresh machine: exit %d\n%s", cur.Exit, output(cur))
	}
}

// TestEnvAdd_concurrentAddsAllKept: N adds at once in one HOME (a CI script
// configuring several clusters in parallel) must all be kept and leave a
// readable file.
func TestEnvAdd_concurrentAddsAllKept(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	cli := isolated(t)
	var wg sync.WaitGroup
	for i := range concurrentAdds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := cli.For(t).Run(t.Context(), "network", "add", fmt.Sprintf("%sc%d", e2eEnvPrefix, i), f.State.GatewayURL)
			if err != nil || res.Exit != exitOK {
				t.Errorf("concurrent env add %d: exit %d, %v\n%s", i, res.Exit, err, output(res))
			}
		}()
	}
	wg.Wait()
	names := map[string]bool{}
	for _, e := range readEnvFile(t, cli).Environments {
		names[e.Name] = true
	}
	for i := range concurrentAdds {
		if n := fmt.Sprintf("%sc%d", e2eEnvPrefix, i); !names[n] {
			t.Errorf("%s was lost by a concurrent env add", n)
		}
	}
	if !names[f.State.Env] {
		t.Errorf("the run's environment %s was lost by concurrent adds", f.State.Env)
	}
}

// networkRow is the row of `orama network list --json` for name.
func networkRow(t *testing.T, cli *oramacli.Runner, name string) map[string]string {
	t.Helper()
	var rows []map[string]string
	if err := json.Unmarshal([]byte(cli.MustOK(t, "network", "list", "--json").Stdout), &rows); err != nil {
		t.Fatalf("orama network list --json: %v", err)
	}
	for _, row := range rows {
		if row["name"] == name {
			return row
		}
	}
	t.Fatalf("orama network list has no row %s", name)
	return nil
}
