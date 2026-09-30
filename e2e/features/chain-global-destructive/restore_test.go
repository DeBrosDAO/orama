//go:build e2e_fleet

package chainglobaldestructive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// localFileMode is the mode of the files the operator's machine holds.
const localFileMode = 0o600

// resealedBundle turns the sealed key backup on n into a migration bundle for
// recipient on the operator's machine (`orama global validator reseal`, with
// the operator's private key in a mode 0600 file) and returns its bytes.
func resealedBundle(t *testing.T, sealed []byte, opPriv, recipient string) []byte {
	t.Helper()
	dir := harness.WorkTemp(t)
	backup, identity, bundle := filepath.Join(dir, "backup"), filepath.Join(dir, "identity"), filepath.Join(dir, "bundle")
	for path, data := range map[string][]byte{backup: sealed, identity: []byte(opPriv)} {
		if err := os.WriteFile(path, data, localFileMode); err != nil {
			t.Fatal(err)
		}
	}
	res := infra.Run(t, harness.CLI(t), "global", "validator", "reseal", "--from", backup, "--identity-file", identity, "--recipient", recipient, "--to", bundle)
	infra.ExpectExit(t, res, infra.ExitOK, "wrote "+bundle)
	out, err := os.ReadFile(bundle)
	if err != nil || len(out) == 0 {
		t.Fatalf("reseal wrote no bundle: %v", err)
	}
	return out
}

// TestValidatorRestore_resealedBackupImportsWithAFloor: the key backup of
// `export-key` (sealed to the operator's key) is resealed on the operator's
// machine to the host's migration key and imported with `--old-host-destroyed
// --floor-height <latest committed height>`: without the two flags, or with
// only one, the import is refused, a bundle with no sign state never
// installs without a floor; with them the key is kept (it is the key already
// here), a floor at the next height is recorded, `check-sign-floor` passes,
// the migration key is used up, and `global start` brings the validator back
// on a chain that moves with its invariants intact. The old host is this
// host, so it is destroyed only in the sense that its chain is stopped.
func TestValidatorRestore_resealedBackupImportsWithAFloor(t *testing.T) {
	c := chain.New(t)
	n := victim(t, c)
	c.AdvanceAtCleanup(t)
	backup := infra.TmpKeyBackup + chain.UniqueID(t, "")
	t.Cleanup(func() { c.CleanupExec(t, n, "rm -f -- "+fleet.ShellQuote(backup)) })
	opPub, opPriv := chain.NewX25519(t)
	infra.ExpectNodeExit(t, "export-key", orama(t, c, n, "global", "validator", "export-key", "--recipient", opPub, "--to", backup), infra.ExitOK, "wrote "+backup)
	recipient := prepareMigration(t, c, n)
	bundlePath := infra.TmpRestoreBundle + chain.UniqueID(t, "")
	t.Cleanup(func() { c.CleanupExec(t, n, "rm -f -- "+fleet.ShellQuote(bundlePath)) })
	c.F.WriteFile(t, n, bundlePath, resealedBundle(t, c.F.ReadFile(t, n, backup), opPriv, recipient), localFileMode)
	restoreAtCleanup(t, c, n, "")
	floor := c.MustStatus(t, n).Height
	infra.ExpectNodeExit(t, "stop chain", orama(t, c, n, "global", "stop", "chain"), infra.ExitOK)
	infra.ExpectNodeExit(t, "a restored backup with no floor", orama(t, c, n, "global", "validator", "migrate", "import", "--from", bundlePath),
		infra.ExitFailure, "restored backup with no sign state")
	infra.ExpectNodeExit(t, "a floor that is not a height", orama(t, c, n, "global", "validator", "migrate", "import", "--from", bundlePath,
		"--old-host-destroyed", "--floor-height", "-1"), infra.ExitUsage, "floor height")
	imp := orama(t, c, n, "global", "validator", "migrate", "import", "--from", bundlePath, "--old-host-destroyed", "--floor-height", itoa(floor))
	infra.ExpectNodeExit(t, "a restored backup with a floor", imp, infra.ExitOK, "sign floor recorded at height "+itoa(floor+1))
	infra.RequireStat(t, c.F, n, keyFile, chain.ServiceUser, chain.ServiceUser, sealedMode)
	if exists(t, c, n, migrationKey) {
		t.Errorf("%s: the migration key is still there after the import used it", n.Name)
	}
	infra.ExpectNodeExit(t, "check-sign-floor after the restore", orama(t, c, n, "global", "validator", "check-sign-floor"), infra.ExitOK)
	infra.ExpectNodeExit(t, "a second import", orama(t, c, n, "global", "validator", "migrate", "import", "--from", bundlePath,
		"--old-host-destroyed", "--floor-height", itoa(floor)), infra.ExitFailure, "no migration key on this host")
	infra.ExpectNodeExit(t, "start after the restore", orama(t, c, n, "global", "start"), infra.ExitOK)
	requireChainBack(t, c, n, "a validator key restore")
}
