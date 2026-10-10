//go:build e2e_fleet

package chainglobal

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/chain"
	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Where the global state root keeps the prepared migration key
// (core/pkg/globalnode/validator.go) and what mode sealed files get.
const (
	migrationKeyPath = "/var/lib/orama-global/migrate-recipient.key"
	sealedMode       = 0o600
	sealedModeOctal  = "600"
	// looseMode is a file other users can read: an identity file must not be.
	looseMode = 0o644
)

// hex64 is an X25519 key as the commands print and take it.
var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// prepareMigration runs `migrate prepare` on n, cancels the prepared key at
// cleanup, and returns the public key it printed.
func prepareMigration(t *testing.T, c *chain.Chain, n fleet.Node) string {
	t.Helper()
	t.Cleanup(func() { c.CleanupExec(t, n, infra.OramaCommand("maint", "global", "validator", "migrate", "cancel")) })
	out := asRoot(t, c.F, n, "maint", "global", "validator", "migrate", "prepare")
	infra.ExpectNodeExit(t, n.Name+" migrate prepare", out, infra.ExitOK)
	key := strings.TrimSpace(out.Stdout)
	if !hex64.MatchString(key) {
		t.Fatalf("%s: migrate prepare printed %q, want 64 hex characters", n.Name, key)
	}
	return key
}

// TestValidatorMigrate_prepareIsIdempotentAndCancelRemovesTheKey: `migrate
// prepare` (run as root on the new host) creates a migration key in the
// global state root and prints its public half; running it again prints the
// same key; `migrate cancel` removes it and says so, and cancelling again says
// nothing was prepared. The key file is root's and mode 0600, and an
// unprivileged account is refused both commands.
func TestValidatorMigrate_prepareIsIdempotentAndCancelRemovesTheKey(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	n := c.Node(t, 2)
	first := prepareMigration(t, c, n)
	again := asRoot(t, c.F, n, "maint", "global", "validator", "migrate", "prepare")
	infra.ExpectNodeExit(t, "a second prepare", again, infra.ExitOK)
	if got := strings.TrimSpace(again.Stdout); got != first {
		t.Errorf("a second prepare printed %q, want the same key %q", got, first)
	}
	infra.RequireStat(t, c.F, n, migrationKeyPath, "root", "root", sealedModeOctal)
	infra.ExpectNodeExit(t, "prepare as an unprivileged account", infra.OnNodeUnprivileged(t, c.F, n, "maint", "global", "validator", "migrate", "prepare"), infra.ExitUsage, infra.MustBeRoot)
	infra.ExpectNodeExit(t, "cancel as an unprivileged account", infra.OnNodeUnprivileged(t, c.F, n, "maint", "global", "validator", "migrate", "cancel"), infra.ExitUsage, infra.MustBeRoot)
	infra.ExpectNodeExit(t, "cancel", asRoot(t, c.F, n, "maint", "global", "validator", "migrate", "cancel"), infra.ExitOK, "migration key removed")
	if _, ok := infra.StatFile(t, c.F, n, migrationKeyPath); ok {
		t.Errorf("%s: %s exists after cancel", n.Name, migrationKeyPath)
	}
	infra.ExpectNodeExit(t, "a second cancel", asRoot(t, c.F, n, "maint", "global", "validator", "migrate", "cancel"), infra.ExitOK, "no migration was prepared on this host")
}

// TestValidatorKeyBackup_exportedSealedAndResealedForANewHost: `export-key`
// (as root on the validator) writes priv_validator_key.json sealed to the
// operator's X25519 public key, a new mode 0600 file that holds no plain JSON
// key; `reseal`, on the operator's machine with the private key in a mode 0600
// file, opens it and seals the key to the new host's migration key. Wrong
// recipients, an existing --to, a wrong identity, an identity file other users
// can read and missing flags are refused, and nothing is written.
func TestValidatorKeyBackup_exportedSealedAndResealedForANewHost(t *testing.T) {
	t.Parallel()
	c := chain.New(t)
	// Not node 2: TestValidatorMigrate_prepareIsIdempotentAndCancelRemovesTheKey
	// owns that node's migration key while this test runs.
	source, target := c.Node(t, 1), c.Node(t, 0)
	opPub, opPriv := chain.NewX25519(t)
	backup := infra.TmpKeyBackup + chain.UniqueID(t, "")
	t.Cleanup(func() { c.CleanupExec(t, source, "rm -f -- "+fleet.ShellQuote(backup)) })
	infra.ExpectNodeExit(t, "export-key", asRoot(t, c.F, source, "maint", "global", "validator", "export-key", "--recipient", opPub, "--to", backup),
		infra.ExitOK, "wrote "+backup)
	infra.RequireStat(t, c.F, source, backup, "root", "root", sealedModeOctal)
	infra.ExpectNodeExit(t, "export-key onto an existing file", asRoot(t, c.F, source, "maint", "global", "validator", "export-key", "--recipient", opPub, "--to", backup), infra.ExitFailure, "create")
	infra.ExpectNodeExit(t, "export-key to a short recipient", asRoot(t, c.F, source, "maint", "global", "validator", "export-key", "--recipient", "abcd", "--to", backup+".x"), infra.ExitUsage, "64 hex characters")
	infra.ExpectNodeExit(t, "export-key with no --to", asRoot(t, c.F, source, "maint", "global", "validator", "export-key", "--recipient", opPub), infra.ExitUsage)
	infra.ExpectNodeExit(t, "export-key as an unprivileged account", infra.OnNodeUnprivileged(t, c.F, source, "maint", "global", "validator", "export-key", "--recipient", opPub, "--to", backup+".y"), infra.ExitUsage, infra.MustBeRoot)
	sealed := c.F.ReadFile(t, source, backup)
	if len(sealed) == 0 || bytes.HasPrefix(bytes.TrimSpace(sealed), []byte("{")) {
		t.Fatalf("the key backup is empty or plain JSON (%d bytes)", len(sealed))
	}
	resealFrom(t, sealed, opPriv, prepareMigration(t, c, target))
}

// resealFrom runs `reseal` locally on a copy of the sealed backup.
func resealFrom(t *testing.T, sealed []byte, opPriv, recipient string) {
	t.Helper()
	dir := harness.WorkTemp(t)
	backupFile, identity, loose, bundle := filepath.Join(dir, "backup"), filepath.Join(dir, "identity"), filepath.Join(dir, "loose"), filepath.Join(dir, "bundle")
	_, otherPriv := chain.NewX25519(t)
	other := filepath.Join(dir, "other")
	write := func(path, data string, mode os.FileMode) {
		if err := os.WriteFile(path, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(identity, opPriv, sealedMode)
	write(loose, opPriv, looseMode)
	write(other, otherPriv, sealedMode)
	write(backupFile, string(sealed), sealedMode)
	args := func(id, to string) []string {
		return []string{"maint", "global", "validator", "reseal", "--from", backupFile, "--identity-file", id, "--recipient", recipient, "--to", to}
	}
	res := infra.Run(t, harness.CLI(t), args(identity, bundle)...)
	infra.ExpectExit(t, res, infra.ExitOK, "wrote "+bundle)
	got, err := os.ReadFile(bundle)
	if err != nil || len(got) == 0 || bytes.Equal(got, sealed) {
		t.Fatalf("the resealed bundle is empty or the backup itself (%d bytes, %v)", len(got), err)
	}
	if st, err := os.Stat(bundle); err != nil || st.Mode().Perm() != sealedMode {
		t.Errorf("the resealed bundle is %v (%v), want mode %o", st, err, sealedMode)
	}
	infra.ExpectExit(t, infra.Run(t, harness.CLI(t), args(identity, bundle)...), infra.ExitFailure, "create")
	infra.ExpectExit(t, infra.Run(t, harness.CLI(t), args(other, bundle+".2")...), infra.ExitFailure, "open the key backup")
	infra.ExpectExit(t, infra.Run(t, harness.CLI(t), args(loose, bundle+".3")...), infra.ExitUsage, "chmod 600")
	infra.ExpectExit(t, infra.Run(t, harness.CLI(t), args(filepath.Join(dir, "absent"), bundle+".4")...), infra.ExitUsage, "--identity-file")
	infra.ExpectExit(t, infra.Run(t, harness.CLI(t), "maint", "global", "validator", "reseal", "--from", backupFile), infra.ExitUsage)
	for _, refused := range []string{".2", ".3", ".4"} {
		if _, err := os.Stat(bundle + refused); err == nil {
			t.Errorf("a refused reseal wrote %s", bundle+refused)
		}
	}
}
