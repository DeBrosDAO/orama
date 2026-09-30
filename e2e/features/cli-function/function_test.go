//go:build e2e_fleet

package clifunction

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// absentFunction is a valid name no test deploys.
const absentFunction = "e2e-absent-fn"

// TestConformance_functionCommands runs the generic checks (help matches
// docs/CLI_REFERENCE.md, --json accepted, unknown flags and subcommands are
// usage errors, every <name> argument required and no surplus accepted) on
// every `orama function` command.
func TestConformance_functionCommands(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	cliconf.Conformance(t, cli, cli.NoWallet(t), cliconf.LoadReference(t), "orama function")
}

// readCommands are the function commands that only read, each naming the
// absent function where it takes one.
var readCommands = [][]string{
	{"function", "list"},
	{"function", "get", absentFunction},
	{"function", "versions", absentFunction},
	{"function", "logs", absentFunction},
	{"function", "secrets", "list"},
	{"function", "triggers", "list", absentFunction},
}

// TestFunctionCommands_noCredentialIsAuthError: with no stored credential
// every function command that calls the gateway exits with the auth code and
// says to sign in (clierr CodeAuth).
func TestFunctionCommands_noCredentialIsAuthError(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t).Isolated(t)
	for _, args := range append(readCommands, []string{"function", "invoke", absentFunction}) {
		res := infra.Run(t, cli, args...)
		if res.Exit != infra.ExitAuth || !strings.Contains(res.Stdout+res.Stderr, "orama auth login") {
			t.Errorf("orama %v with no credential: exit %d, want %d\n%s%s", args, res.Exit, infra.ExitAuth, res.Stdout, res.Stderr)
		}
	}
}

// TestFunctionCommands_emptyNamespace: in a fresh namespace nothing is
// deployed: list says so (and --json is an empty array), and every command
// naming a function that does not exist is "not found" (exit 4).
func TestFunctionCommands_emptyNamespace(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	if out := n.CLI.MustOK(t, "function", "list").Stdout; !strings.Contains(out, "No functions deployed") {
		t.Errorf("function list in a fresh namespace:\n%s", out)
	}
	var rows []map[string]string
	if err := oramacli.DecodeJSON(n.CLI.MustOK(t, "function", "list", "--json"), &rows); err != nil || len(rows) != 0 {
		t.Errorf("function list --json in a fresh namespace: %v, %d rows", err, len(rows))
	}
	n.CLI.MustOK(t, "function", "secrets", "list")
	for _, args := range append(readCommands[1:4:4], []string{"function", "invoke", absentFunction},
		[]string{"function", "disable", absentFunction}, []string{"function", "triggers", "list", absentFunction}) {
		res := infra.Run(t, n.CLI, args...)
		if res.Exit != infra.ExitNotFound {
			t.Errorf("orama %v of an absent function: exit %d, want %d\n%s%s", args, res.Exit, infra.ExitNotFound, res.Stdout, res.Stderr)
		}
	}
}

// TestFunctionCommands_nameIsValidated: a function name is a letter then
// letters, digits, hyphens or underscores (functions/helpers.go
// validNameRegex, which init enforces). Every command that takes a name must
// refuse one that is not, as a usage error, rather than put it in a request
// path ("../" walks to another route).
func TestFunctionCommands_nameIsValidated(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t).Isolated(t)
	for _, name := range []string{"../../v1/namespace/list", "a/b", "9starts-with-digit", "has space", "tab\tname", "rtl‮"} {
		for _, verb := range []string{"get", "versions", "invoke", "logs", "enable"} {
			res := infra.Run(t, cli, "function", verb, name)
			if res.Exit != infra.ExitUsage {
				t.Errorf("function %s %q: exit %d, want %d\n%s%s", verb, name, res.Exit, infra.ExitUsage, res.Stdout, res.Stderr)
			}
		}
	}
}

// TestFunctionTriggersAdd_needsExactlyOneSource: --topic and --schedule are
// mutually exclusive and one is required (functions/triggers.go); both
// mistakes are usage errors, found before any request.
func TestFunctionTriggersAdd_needsExactlyOneSource(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t).NoWallet(t)
	for _, args := range [][]string{
		{"function", "triggers", "add", absentFunction},
		{"function", "triggers", "add", absentFunction, "--topic", "t", "--schedule", "0 3 * * *"},
	} {
		infra.ExpectExit(t, infra.Run(t, cli, args...), infra.ExitUsage)
	}
}

// TestFunctionInit_scaffoldsProject: init creates <name>/function.yaml and
// <name>/function.go in the working directory, refuses to overwrite, and
// refuses names that are not a function name, writing nothing
// (docs/CLI_REFERENCE.md#orama-function-init, docs/SERVERLESS.md).
func TestFunctionInit_scaffoldsProject(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t).NoWallet(t)
	const name = "e2e-hello"
	cli.MustOK(t, "function", "init", name)
	cfg, err := os.ReadFile(filepath.Join(cli.Home, name, "function.yaml"))
	if err != nil || !strings.Contains(string(cfg), "name: "+name) {
		t.Errorf("function.yaml: %v\n%s", err, cfg)
	}
	if src, err := os.ReadFile(filepath.Join(cli.Home, name, "function.go")); err != nil || !strings.Contains(string(src), "fn.Run") {
		t.Errorf("function.go: %v", err)
	}
	infra.ExpectRefused(t, infra.Run(t, cli, "function", "init", name), "already exists")
	before, err := os.ReadDir(cli.Home)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../escape", "a/b", "9lives", "semi;colon", "‮evil"} {
		infra.ExpectRefused(t, infra.Run(t, cli, "function", "init", bad), "invalid function name")
	}
	after, err := os.ReadDir(cli.Home)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("refused inits changed the working directory: %d -> %d entries", len(before), len(after))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cli.Home), "escape")); err == nil {
		t.Error("function init ../escape wrote outside the working directory")
	}
}
