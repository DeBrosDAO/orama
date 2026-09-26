package node

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/upgrade"
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

// --public-ip reaches the orchestrator, which records it as node.public_ip.
func TestUpgrade_PublicIPFlagIsParsed(t *testing.T) {
	upgradeFlags = upgrade.Flags{}

	if err := runParse(t, upgradeCmd, "--public-ip", "203.0.113.7"); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if upgradeFlags.PublicIP != "203.0.113.7" {
		t.Errorf("PublicIP = %q", upgradeFlags.PublicIP)
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

// The Anyone network was removed; every node installs the Tor client, so none
// of the --anyone-* flags choose anything any more. An operator who still
// passes one must be told why, not silently ignored.
func TestInstallAndUpgrade_RemovedAnyoneFlagsAreRejectedWithAReason(t *testing.T) {
	cases := map[*cobra.Command][]string{
		installCmd: {"--anyone-client", "--anyone-relay", "--anyone-migrate"},
		// upgrade's --anyone-client is refused in RunE; see the tests below.
		upgradeCmd: {"--anyone-relay", "--anyone-migrate"},
	}
	for cmd, flags := range cases {
		for _, flag := range flags {
			err := runParse(t, cmd, flag)
			if err == nil {
				t.Fatalf("%s %s: the removed flag must be an error", cmd.Name(), flag)
			}
			if !strings.Contains(err.Error(), strings.TrimPrefix(flag, "--")) {
				t.Errorf("%s %s: the error should name the flag, got: %v", cmd.Name(), flag, err)
			}
			if !strings.Contains(err.Error(), "Tor client is installed on every node") {
				t.Errorf("%s %s: the error should say what replaced it, got: %v", cmd.Name(), flag, err)
			}
		}
	}
}

// An operator passing --anyone-client to upgrade is refused with the reason.
func TestCheckUpgradeAnyoneClient_operatorIsRefused(t *testing.T) {
	err := checkUpgradeAnyoneClient(true, false)
	if err == nil {
		t.Fatal("--anyone-client from an operator must be an error")
	}
	if !strings.Contains(err.Error(), "anyone-client") || !strings.Contains(err.Error(), "Tor client is installed on every node") {
		t.Errorf("error should name the flag and what replaced it: %v", err)
	}
}

// The upgrade that introduces Tor is started by the old binary, which accepts
// --anyone-client and forwards its argv to the new binary after swapping it
// in. Refusing it there would abort the upgrade with services stopped.
func TestCheckUpgradeAnyoneClient_reexecCarriesItThrough(t *testing.T) {
	if err := checkUpgradeAnyoneClient(true, true); err != nil {
		t.Errorf("--anyone-client forwarded by the re-exec must be accepted: %v", err)
	}
	if err := checkUpgradeAnyoneClient(false, false); err != nil {
		t.Errorf("no flag must be fine: %v", err)
	}
}

func TestUpgrade_ReexecAnyoneClientFlagParsesAndIsHidden(t *testing.T) {
	upgradeAnyoneClient = false
	t.Cleanup(func() { upgradeAnyoneClient = false })
	if err := runParse(t, upgradeCmd, "--anyone-client", "--reexeced-after-binary-swap"); err != nil {
		t.Fatalf("the re-exec argv must parse: %v", err)
	}
	if !upgradeAnyoneClient {
		t.Error("flag value not surfaced")
	}
	if f := upgradeCmd.Flags().Lookup("anyone-client"); f == nil || !f.Hidden {
		t.Error("--anyone-client must stay hidden from help")
	}
}

// Any other unknown flag keeps cobra's plain error.
func TestExplainRemovedFlags_leavesOtherErrorsAlone(t *testing.T) {
	err := runParse(t, installCmd, "--no-such-flag")
	if err == nil {
		t.Fatal("an unknown flag must be an error")
	}
	if strings.Contains(err.Error(), "Anyone") {
		t.Errorf("an unrelated unknown flag must not mention Anyone: %v", err)
	}
}
