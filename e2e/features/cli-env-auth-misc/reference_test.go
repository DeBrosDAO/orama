//go:build e2e_fleet

package clienvauthmisc

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/cliconf"
	"github.com/DeBrosOfficial/network/e2e/harness"
)

// TestCLIReference_liveTreeMatchesReference walks the binary under test from
// `orama --help` down and compares its command tree with docs/whitepaper/technical-reference/appendices/d-cli-reference.md:
// the reference claims to be generated from the tree and unable to drift
// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md, header), so the binary the fleet runs must show
// exactly the documented commands with the documented one-line descriptions.
func TestCLIReference_liveTreeMatchesReference(t *testing.T) {
	t.Parallel()
	ref := cliconf.LoadReference(t)
	live := cliconf.WalkLive(t, harness.CLI(t))
	if len(live) == 0 {
		t.Fatal("orama --help lists no command")
	}
	onlyLive, onlyDoc, shorts := cliconf.TreeDiff(ref, live)
	if len(onlyLive) > 0 {
		t.Errorf("the binary has commands %s does not document:\n  %s", cliconf.ReferencePath, strings.Join(onlyLive, "\n  "))
	}
	if len(onlyDoc) > 0 {
		t.Errorf("%s documents commands the binary does not have:\n  %s", cliconf.ReferencePath, strings.Join(onlyDoc, "\n  "))
	}
	if len(shorts) > 0 {
		t.Errorf("one-line descriptions differ:\n  %s", strings.Join(shorts, "\n  "))
	}
}

// TestCLIReference_everyCommandHelpMatches compares every documented
// command's `--help` (flags, subcommands, aliases, usage line, the inherited
// --json) with its reference section, so a flag added, renamed or removed
// without regenerating the reference fails here with the section to fix.
func TestCLIReference_everyCommandHelpMatches(t *testing.T) {
	t.Parallel()
	ref := cliconf.LoadReference(t)
	cli := harness.CLI(t)
	for _, c := range ref.Commands {
		t.Run(strings.Join(c.Args(), "_"), func(t *testing.T) {
			t.Parallel()
			cliconf.CheckHelp(t, cli.For(t), ref, c)
		})
	}
}

// TestCLIReference_unknownTopLevelCommandIsUsage: a command name the binary
// does not have is a usage error (exit 2) naming it, the same answer as a
// mistyped subcommand one level down (core/cmd/orama/root.go runCLI).
func TestCLIReference_unknownTopLevelCommandIsUsage(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	for _, args := range [][]string{
		{cliconf.BogusSubcommand},
		{cliconf.BogusSubcommand, "--help"},
		{"ENV"},       // commands are case-sensitive
		{"env\u202e"}, // RTL override glued to a real name
		{"e\u0301nv"}, // combining accent inside a real name
	} {
		res := run(t, cli, args...)
		if res.Exit != exitUsage {
			t.Errorf("orama %q: exit %d, want %d\n%s%s", args, res.Exit, exitUsage, res.Stdout, res.Stderr)
		}
	}
}

// TestConformance_envAuthMiscCommands runs the generic checks (help matches
// the reference, --json accepted, unknown flag and subcommand are usage
// errors, required positional arguments enforced) on the commands this
// package owns.
func TestConformance_envAuthMiscCommands(t *testing.T) {
	t.Parallel()
	cli := harness.CLI(t)
	cliconf.Conformance(t, cli, cli.NoWallet(t), cliconf.LoadReference(t),
		"orama env", "orama auth", "orama inspect", "orama ssh", "orama rollout", "orama version")
}
