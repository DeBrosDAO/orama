package node

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/cli/production/upgrade"
	"github.com/spf13/cobra"
)

// These commands used to parse their own arguments with the stdlib flag
// package behind DisableFlagParsing, which is why several of them exited 1 on
// --help and accepted only single-dash flags. Now that cobra owns parsing,
// these tests drive the real commands rather than a parser that no longer runs.

// runParse parses args for a subcommand without executing it, and returns any
// parse error. Execution must go through the parent: cobra resolves a
// subcommand's args from its root, so calling Execute on the child parses them
// against "node" instead.
func runParse(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	original := cmd.RunE
	originalRun := cmd.Run
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	cmd.Run = nil
	t.Cleanup(func() {
		cmd.RunE = original
		cmd.Run = originalRun
		Cmd.SetArgs(nil)
	})

	var out bytes.Buffer
	Cmd.SetOut(&out)
	Cmd.SetErr(&out)
	Cmd.SetArgs(append([]string{cmd.Name()}, args...))
	return Cmd.Execute()
}

// The orchestrator sets this flag on its own argv when it re-execs after
// swapping the binary. If the registration is ever dropped to tidy the help
// output, the re-execed process fails with "unknown flag" and the upgrade
// breaks half way through.
func TestUpgrade_HiddenReexecFlagIsAccepted(t *testing.T) {
	upgradeFlags = upgrade.Flags{}

	if err := runParse(t, upgradeCmd, "--reexeced-after-binary-swap"); err != nil {
		t.Fatalf("the hidden re-exec flag must parse: %v", err)
	}
	if !upgradeFlags.ReexecedAfterBinarySwap {
		t.Error("flag value not surfaced on the Flags struct")
	}

	// It must stay hidden: an operator has no reason to be offered it, and
	// passing it by hand skips the phases that install the new binary.
	if f := upgradeCmd.Flags().Lookup("reexeced-after-binary-swap"); f == nil || !f.Hidden {
		t.Error("the re-exec flag must be registered and hidden")
	}
}

// Defaulting this to true would make the very first operator-initiated upgrade
// skip the phases that install the binary.
func TestUpgrade_HiddenReexecFlagDefaultsFalse(t *testing.T) {
	upgradeFlags = upgrade.Flags{}

	if err := runParse(t, upgradeCmd); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if upgradeFlags.ReexecedAfterBinarySwap {
		t.Error("must default to false")
	}
}

// Nameserver is a pointer so the orchestrator can tell "not given" (keep the
// saved preference) from an explicit choice.
func TestUpgrade_NameserverStaysUnsetUnlessGiven(t *testing.T) {
	upgradeFlags = upgrade.Flags{}

	if err := runParse(t, upgradeCmd); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if upgradeFlags.Nameserver != nil {
		t.Error("omitting --nameserver must leave the saved preference alone")
	}
}

func TestMigrateRaftID_ParsesItsFlags(t *testing.T) {
	raftIDFlags.Env, raftIDFlags.Node, raftIDFlags.DryRun, raftIDFlags.Force = "", "", false, false

	if err := runParse(t, migrateRaftIDCmd, "--env", "testnet", "--node", "1.2.3.4", "--dry-run"); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if raftIDFlags.Env != "testnet" || raftIDFlags.Node != "1.2.3.4" || !raftIDFlags.DryRun || raftIDFlags.Force {
		t.Fatalf("unexpected flags: %+v", raftIDFlags)
	}
}

// An unknown flag must be rejected rather than silently ignored.
func TestMigrateRaftID_RejectsUnknownFlag(t *testing.T) {
	if err := runParse(t, migrateRaftIDCmd, "--env", "testnet", "--wat"); err == nil {
		t.Fatal("an unknown flag must be an error")
	}
}

// --help must succeed everywhere. `orama node invite --help` used to read the
// node config first and exit 1 on any machine that is not a node, which is
// exactly where an operator reads help before joining one.
func TestHelpSucceedsForEveryNodeSubcommand(t *testing.T) {
	for _, sub := range Cmd.Commands() {
		sub := sub
		t.Run(sub.Name(), func(t *testing.T) {
			var out bytes.Buffer
			Cmd.SetOut(&out)
			Cmd.SetErr(&out)
			Cmd.SetArgs([]string{sub.Name(), "--help"})
			t.Cleanup(func() { Cmd.SetArgs(nil) })
			if err := Cmd.Execute(); err != nil {
				t.Fatalf("--help must succeed, got: %v", err)
			}
		})
	}
}

// --anyone-relay was removed: every node's gateway serves /v1/proxy/anon and
// needs the local SOCKS proxy, so relay mode is not a choice. An operator who
// still passes it must be told, not silently ignored.
func TestInstall_RemovedAnyoneRelayFlagIsRejected(t *testing.T) {
	err := runParse(t, installCmd, "--anyone-relay")
	if err == nil {
		t.Fatal("the removed --anyone-relay flag must be an error")
	}
	if !strings.Contains(err.Error(), "anyone-relay") {
		t.Errorf("the error should name the flag, got: %v", err)
	}
}
