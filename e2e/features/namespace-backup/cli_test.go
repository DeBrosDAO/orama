//go:build e2e_fleet

package namespacebackup

import (
	"bytes"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

var (
	hexKeyLine = regexp.MustCompile(`(?m)^[0-9a-f]{64}$`)
	apiKeyText = regexp.MustCompile(`orama_[A-Za-z0-9_]+`)
)

// Key labels: canaryLabel marks the key a refused restore must leave alone.
const (
	backupLabel = "e2e-backup"
	canaryLabel = "e2e-canary"
)

// cliKey mints a cache key with the namespace's CLI and returns it.
func cliKey(t testing.TB, cli *oramacli.Runner) string {
	t.Helper()
	return cliKeyLabelled(t, cli, backupLabel)
}

func cliKeyLabelled(t testing.TB, cli *oramacli.Runner, label string) string {
	t.Helper()
	k := apiKeyText.FindString(cli.MustOK(t, "namespace", "keys", "create", "--scope", "cache", "--label", label).Stdout)
	if k == "" {
		t.Fatal("orama namespace keys create printed no key")
	}
	return k
}

func keyWorks(t testing.TB, n *ns.Namespace, key string, want bool) func() (bool, error) {
	return func() (bool, error) {
		ok := tenancy.Get(t, n.Client, "/v1/cache/health", tenancy.Cred{APIKey: key}).Status == http.StatusOK
		if ok != want {
			return false, errors.New("not yet")
		}
		return true, nil
	}
}

// requireCanary fails unless `orama namespace keys list`, which reads the
// namespace's own key rows (no gateway cache in between), still lists the
// canary key active: a restore that went through would have replaced the
// key rows with the backup's, which predate it.
func requireCanary(t testing.TB, cli *oramacli.Runner, after string) {
	t.Helper()
	out := cli.MustOK(t, "namespace", "keys", "list").Stdout
	for _, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), canaryLabel) && strings.Contains(line, " active ") {
			return
		}
	}
	t.Fatalf("after %s the canary key is no longer listed active: a refused restore replaced the key rows:\n%s", after, out)
}

func cliRestoreKey(t testing.TB, cli *oramacli.Runner) string {
	t.Helper()
	k := hexKeyLine.FindString(cli.MustOK(t, "namespace", "restore-key").Stdout)
	if k == "" {
		t.Fatal("orama namespace restore-key printed no 64-hex key")
	}
	return k
}

func cliBackup(t testing.TB, cli *oramacli.Runner, k tenancy.BackupKey) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "ns.orbk")
	cli.MustOK(t, "namespace", "backup", "--key", k.PubHex, "--out", out)
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the backup file is %v (%v), want 0600", info.Mode(), err)
	}
	return out
}

// TestBackupCLI_backupAndRestore drives backup, restore-key and restore as an
// operator: the file is sealed (0600, ORBK) to the given key, and restoring it
// brings back the keys that existed and drops one minted after
// (docs/CLI_REFERENCE.md "orama namespace backup", "orama namespace restore").
func TestBackupCLI_backupAndRestore(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	cli, k := n.CLI, tenancy.NewBackupKey(t)
	kept := cliKey(t, cli)
	file := cliBackup(t, cli, k)
	blob, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if p := k.Open(t, blob); p.Namespace != n.Name {
		t.Fatalf("the CLI's backup is of %q", p.Namespace)
	}
	dropped := cliKey(t, cli)
	dest := cliRestoreKey(t, cli)
	if again := cliRestoreKey(t, cli); again != dest {
		t.Fatalf("the restore key changed between two reads: %s, %s", dest, again)
	}
	for range 2 {
		out := cli.MustOK(t, "namespace", "restore", "--in", file, "--key-file", k.File(t), "--namespace", n.Name, "--dest-key", dest).Stdout
		if !strings.Contains(out, "Restored namespace "+n.Name) {
			t.Fatalf("restore printed %q", out)
		}
	}
	eventually.Require(t, pollEvery, keyCacheBudget, "the key from before the backup to work", keyWorks(t, n, kept, true))
	eventually.Require(t, pollEvery, keyCacheBudget, "the key minted after the backup to be gone", keyWorks(t, n, dropped, false))
}

// TestBackupCLI_refusalsSendNothing: a wrong private key, a corrupt file, a
// --namespace that is not the backup's and a malformed key stop on the
// operator's machine; a backup of another namespace is refused by the
// gateway; none of them changes the namespace.
func TestBackupCLI_refusalsSendNothing(t *testing.T) {
	t.Parallel()
	pair := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{Via: ns.ViaOperator})
	a, b := pair[0], pair[1]
	k := tenancy.NewBackupKey(t)
	fileA, fileB := cliBackup(t, a.CLI, k), cliBackup(t, b.CLI, k)
	// Minted after the backups: a restore of either would drop it.
	canary := cliKeyLabelled(t, a.CLI, canaryLabel)
	corrupt := filepath.Join(t.TempDir(), "corrupt.orbk")
	raw, _ := os.ReadFile(fileA)
	raw[len(raw)/2] ^= 0xff
	if err := os.WriteFile(corrupt, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	dest, keyFile, wrongKey := cliRestoreKey(t, a.CLI), k.File(t), tenancy.NewBackupKey(t).File(t)
	cases := map[string][]string{
		"wrong private key":   {"--in", fileA, "--key-file", wrongKey, "--namespace", a.Name, "--dest-key", dest},
		"corrupt file":        {"--in", corrupt, "--key-file", keyFile, "--namespace", a.Name, "--dest-key", dest},
		"--namespace not its": {"--in", fileA, "--key-file", keyFile, "--namespace", b.Name, "--dest-key", dest},
		"another ns's backup": {"--in", fileB, "--key-file", keyFile, "--namespace", b.Name, "--dest-key", dest},
		"malformed dest key":  {"--in", fileA, "--key-file", keyFile, "--namespace", a.Name, "--dest-key", "abc"},
		"missing file":        {"--in", filepath.Join(t.TempDir(), "none"), "--key-file", keyFile, "--namespace", a.Name, "--dest-key", dest},
		"no flags":            {},
	}
	for name, args := range cases {
		res, err := a.CLI.Run(t.Context(), append([]string{"namespace", "restore"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		if res.Exit == 0 {
			t.Errorf("%s: restore succeeded: %s", name, res.Stdout)
		}
		requireCanary(t, a.CLI, name)
	}
	for _, args := range [][]string{{"--key", "abc", "--out", filepath.Join(t.TempDir(), "x")}, {"--key", k.PubHex}} {
		if res, err := a.CLI.Run(t.Context(), append([]string{"namespace", "backup"}, args...)...); err != nil || res.Exit == 0 {
			t.Errorf("backup %v succeeded (%v)", args, err)
		}
	}
	eventually.Require(t, pollEvery, keyCacheBudget, "the canary key to still work", keyWorks(t, a, canary, true))
}

// TestBackupCLI_sealAndOpenAnyFile: backup-seal encrypts any file to a public
// key and backup-open with the private key gives it back byte for byte; the
// wrong key and a file that is not sealed are refused.
func TestBackupCLI_sealAndOpenAnyFile(t *testing.T) {
	t.Parallel()
	cli, dir := harness.CLI(t), t.TempDir()
	k, other := tenancy.NewBackupKey(t), tenancy.NewBackupKey(t)
	for name, size := range map[string]int{"empty": 0, "small": 17, "large": 5 << 20} {
		plain := make([]byte, size)
		if _, err := rand.Read(plain); err != nil {
			t.Fatal(err)
		}
		in, sealed, back := filepath.Join(dir, name), filepath.Join(dir, name+".orbk"), filepath.Join(dir, name+".back")
		if err := os.WriteFile(in, plain, 0o600); err != nil {
			t.Fatal(err)
		}
		cli.MustOK(t, "namespace", "backup-seal", "--key", k.PubHex, "--in", in, "--out", sealed)
		// The private key is a throwaway made for this file; the CLI takes it
		// only as a flag.
		cli.MustOK(t, "namespace", "backup-open", "--key", k.PrivHex(), "--in", sealed, "--out", back)
		if got, err := os.ReadFile(back); err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%s: backup-open gave back %d bytes of %d (%v)", name, len(got), len(plain), err)
		}
		if res, err := cli.Run(t.Context(), "namespace", "backup-open", "--key", other.PrivHex(), "--in", sealed, "--out", back+"2"); err != nil || res.Exit == 0 {
			t.Errorf("%s: opened with the wrong key (%v)", name, err)
		}
		if res, err := cli.Run(t.Context(), "namespace", "backup-open", "--key", k.PrivHex(), "--in", in, "--out", back+"3"); err != nil || res.Exit == 0 {
			t.Errorf("%s: opened a file that was never sealed (%v)", name, err)
		}
	}
}
