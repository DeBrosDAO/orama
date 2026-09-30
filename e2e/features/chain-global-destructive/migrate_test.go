//go:build e2e_fleet

package chainglobaldestructive

import (
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// hexKeyLen is the length of an X25519 key in hex.
const hexKeyLen = 64

// keyMovedTo finds where a migration export left the validator key.
var keyMovedTo = regexp.MustCompile(`key moved to (\S+),`)

// requireKeyGone checks what `migrate export` leaves behind on the old host:
// the chain stopped and disabled, the key out of the chain home (kept, root's,
// mode 0600, at keyCopy), and every way to start the chain refused, so the
// old host cannot sign as the validator again.
func requireKeyGone(t *testing.T, c *chain.Chain, n fleet.Node, keyCopy, bundle string) {
	t.Helper()
	if got := unitState(t, c, n); got == stateActive {
		t.Fatalf("%s: the chain unit is %s after the export", n.Name, got)
	}
	if got := sh(t, c, n, "systemctl is-enabled "+chain.Unit); got != infra.UnitDisabled {
		t.Errorf("%s: the chain unit is %q after the export, want disabled", n.Name, got)
	}
	if exists(t, c, n, keyFile) {
		t.Errorf("%s: %s is still in the chain home after the export", n.Name, keyFile)
	}
	infra.RequireStat(t, c.F, n, keyCopy, "root", "root", sealedMode)
	infra.RequireStat(t, c.F, n, bundle, "root", "root", sealedMode)
	infra.ExpectNodeExit(t, "check-sign-floor after the key left", orama(t, c, n, "global", "validator", "check-sign-floor"), infra.ExitConflict, "refusing to start the chain")
	infra.ExpectNodeExit(t, "start after the key left", orama(t, c, n, "global", "start"), infra.ExitFailure, "refusing to start the chain")
	if got := unitState(t, c, n); got == stateActive {
		t.Fatalf("%s: global start started a chain whose key moved away", n.Name)
	}
	infra.ExpectNodeExit(t, "an export onto an existing bundle", orama(t, c, n, "global", "validator", "migrate", "export",
		"--recipient", strings.Repeat("0", hexKeyLen), "--to", bundle), infra.ExitUsage, "must not exist")
}

// TestValidatorMigrate_sameHostRoundTripKeepsTheKeySafe: on a validator,
// `migrate prepare` prints the migration key, `migrate export --recipient
// <key> --to <file>` stops and disables the chain, seals the key and its
// sign state into the file, records the sign floor and moves the key out of
// the chain home: after it neither `check-sign-floor` nor `global start`
// lets the chain run, and the chain is stopped. `migrate import --from
// <file>` (allowed only while the chain is stopped) puts the key back with
// its state and a floor no lower than the last state signed, uses up the
// migration key (a second import finds none), `check-sign-floor` passes, and
// `global start` brings the validator back: the chain moves on every
// validator with its invariants intact. An import while the chain runs is
// refused. The double sign the guard prevents is the case of the key on two
// hosts; here one host gives the key up and takes it back.
func TestValidatorMigrate_sameHostRoundTripKeepsTheKeySafe(t *testing.T) {
	c := chain.New(t)
	n := victim(t, c)
	c.AdvanceAtCleanup(t)
	bundle := infra.TmpMigrateBundle + chain.UniqueID(t, "")
	t.Cleanup(func() { c.CleanupExec(t, n, "rm -f -- "+fleet.ShellQuote(bundle)) })
	key := prepareMigration(t, c, n)
	restoreAtCleanup(t, c, n, bundle)
	exp := orama(t, c, n, "global", "validator", "migrate", "export", "--recipient", key, "--to", bundle)
	infra.ExpectNodeExit(t, "migrate export", exp, infra.ExitOK, "chain stopped and disabled at", "copy "+bundle)
	moved := keyMovedTo.FindStringSubmatch(exp.Stdout)
	if moved == nil {
		t.Fatalf("migrate export does not say where the key went:\n%s", exp.Stdout)
	}
	requireKeyGone(t, c, n, moved[1], bundle)
	imp := orama(t, c, n, "global", "validator", "migrate", "import", "--from", bundle)
	infra.ExpectNodeExit(t, "migrate import", imp, infra.ExitOK, "sign floor recorded at", "orama global start")
	infra.RequireStat(t, c.F, n, keyFile, chain.ServiceUser, chain.ServiceUser, sealedMode)
	if exists(t, c, n, migrationKey) {
		t.Errorf("%s: the migration key is still there after the import used it", n.Name)
	}
	infra.ExpectNodeExit(t, "check-sign-floor after the import", orama(t, c, n, "global", "validator", "check-sign-floor"), infra.ExitOK)
	infra.ExpectNodeExit(t, "a second import", orama(t, c, n, "global", "validator", "migrate", "import", "--from", bundle), infra.ExitFailure, "no migration key on this host")
	infra.ExpectNodeExit(t, "start after the import", orama(t, c, n, "global", "start"), infra.ExitOK)
	requireChainBack(t, c, n, "a validator key migration round trip")
	infra.ExpectNodeExit(t, "an import while the chain runs", orama(t, c, n, "global", "validator", "migrate", "import", "--from", bundle),
		infra.ExitConflict, "stop it first")
}

// TestValidatorMigrate_importFlagsAreChecked: a migration bundle takes
// neither restore flag and a restored backup needs both; the flags go
// together, need a bundle, and take a positive height. Each mistake is a
// usage error before anything is read.
func TestValidatorMigrate_importFlagsAreChecked(t *testing.T) {
	c := chain.New(t)
	n := victim(t, c)
	const from = infra.NoSuchFile
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no --from", nil, "--from is required"},
		{"--old-host-destroyed without a floor", []string{"--from", from, "--old-host-destroyed"}, "go together"},
		{"a floor without --old-host-destroyed", []string{"--from", from, "--floor-height", "5"}, "go together"},
		{"a negative floor", []string{"--from", from, "--old-host-destroyed", "--floor-height", "-3"}, "floor height"},
	}
	for _, tc := range cases {
		out := orama(t, c, n, append([]string{"global", "validator", "migrate", "import"}, tc.args...)...)
		infra.ExpectNodeExit(t, tc.name, out, infra.ExitUsage, tc.want)
	}
	infra.ExpectNodeExit(t, "an export with a short recipient", orama(t, c, n, "global", "validator", "migrate", "export", "--recipient", "abcd", "--to", infra.TmpKeyBackup+"none"),
		infra.ExitUsage, "64 hex characters")
	if got := unitState(t, c, n); got != stateActive {
		t.Errorf("%s: the chain unit is %s after refused imports and exports", n.Name, got)
	}
}
