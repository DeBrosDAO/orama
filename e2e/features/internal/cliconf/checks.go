//go:build e2e_fleet

package cliconf

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// JSONFlag is the root's persistent machine-output flag
// (core/cmd/orama/internal/printer/context.go JSONFlag).
const JSONFlag = "json"

// Arguments no command accepts.
const (
	BogusFlag       = "--e2e-no-such-flag"
	BogusSubcommand = "e2e-no-such-subcommand"
	bogusArg        = "e2e-surplus-argument"
)

// HelpOf runs `orama <path> --help` and parses it. Help must succeed, print
// on stdout and say nothing on stderr (a deprecated command's notice aside).
func HelpOf(t testing.TB, cli *oramacli.Runner, c Command) Help {
	t.Helper()
	res := infra.Run(t, cli, append(c.Args(), "--help")...)
	infra.ExpectExit(t, res, infra.ExitOK)
	// A deprecated command's notice goes to stderr, as it should.
	if strings.TrimSpace(res.Stderr) != "" && !strings.HasPrefix(c.Short, deprecatedPrefix) {
		t.Errorf("%s --help wrote to stderr: %s", c.Path, res.Stderr)
	}
	if !strings.Contains(res.Stdout, secUsage) {
		t.Fatalf("%s --help printed no %q section:\n%s", c.Path, secUsage, res.Stdout)
	}
	return ParseHelp(res.Stdout)
}

// CheckHelp fails when the binary's help for c disagrees with the reference.
func CheckHelp(t testing.TB, cli *oramacli.Runner, ref *Reference, c Command) {
	t.Helper()
	for _, d := range Diff(ref, c, HelpOf(t, cli, c)) {
		t.Errorf("%s drifted from %s: %s", c.Path, c.Anchor(), d)
	}
}

// CheckUnknownFlag: a flag the command does not define is a usage error
// (exit 2) naming the flag, whatever the command would otherwise do.
func CheckUnknownFlag(t testing.TB, cli *oramacli.Runner, c Command) {
	t.Helper()
	res := infra.Run(t, cli, append(c.Args(), BogusFlag)...)
	infra.ExpectExit(t, res, infra.ExitUsage, strings.TrimPrefix(BogusFlag, "--"))
}

// runnableGroups are groups that do something of their own without a
// subcommand, so running them bare is not "print the help".
var runnableGroups = map[string]bool{
	"orama auth sessions": true, // lists the wallet's sessions (cmd/authcmd/auth.go)
	"orama monitor":       true, // opens the live view (cmd/monitorcmd/monitor.go runLive)
}

// CheckGroup: a group with no argument prints its help and succeeds; an
// unknown subcommand is a usage error that names what was typed
// (core/cmd/orama/root.go classifyUsageErrors).
func CheckGroup(t testing.TB, cli *oramacli.Runner, c Command) {
	t.Helper()
	if !runnableGroups[c.Path] {
		res := infra.Run(t, cli, c.Args()...)
		infra.ExpectExit(t, res, infra.ExitOK, secCommands)
	}
	res := infra.Run(t, cli, append(c.Args(), BogusSubcommand)...)
	infra.ExpectExit(t, res, infra.ExitUsage, BogusSubcommand)
}

// CheckArity: a leaf command whose usage declares required <args> refuses
// fewer with the usage code, and, when its usage is closed, one more. A
// command whose usage declares no positional argument is not probed: without
// a validator cobra would run it, and some of those build, provision or erase.
// Run it with a NoWallet runner: a command that skipped validation then still
// cannot authenticate to anything.
func CheckArity(t testing.TB, noWallet *oramacli.Runner, c Command) {
	t.Helper()
	p := PositionalOf(c)
	if p.Required == 0 {
		return
	}
	res := infra.Run(t, noWallet, c.Args()...)
	infra.ExpectExit(t, res, infra.ExitUsage)
	if !p.Open {
		args := c.Args()
		for range p.Required {
			args = append(args, "e2e-arg")
		}
		res := infra.Run(t, noWallet, append(args, bogusArg)...)
		infra.ExpectExit(t, res, infra.ExitUsage)
	}
}

// CheckHelpWithJSON: --json is accepted by every command, so --help --json
// still prints help and succeeds.
func CheckHelpWithJSON(t testing.TB, cli *oramacli.Runner, c Command) {
	t.Helper()
	res := infra.Run(t, cli, append(c.Args(), "--"+JSONFlag, "--help")...)
	infra.ExpectExit(t, res, infra.ExitOK, secUsage)
}

// Conformance runs every generic check on every documented command under
// the given paths, one subtest per command. cli is the operator's runner
// (help only: nothing reaches a gateway); noWallet runs the arity checks.
func Conformance(t *testing.T, cli, noWallet *oramacli.Runner, ref *Reference, paths ...string) {
	t.Helper()
	for _, root := range paths {
		cmds := ref.Under(root)
		if len(cmds) == 0 {
			t.Fatalf("%s documents no %q", ReferencePath, root)
		}
		for _, c := range cmds {
			t.Run(strings.Join(c.Args(), "_"), func(t *testing.T) {
				t.Parallel()
				CheckHelp(t, cli, ref, c)
				CheckHelpWithJSON(t, cli, c)
				CheckUnknownFlag(t, cli, c)
				if c.Group() {
					CheckGroup(t, cli, c)
					return
				}
				CheckArity(t, noWallet, c)
			})
		}
	}
}
