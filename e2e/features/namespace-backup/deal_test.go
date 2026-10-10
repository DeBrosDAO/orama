//go:build e2e_fleet

package namespacebackup

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	dealReplicas = 2
	dealKeyBytes = 32
)

var pieceFlag = regexp.MustCompile(`--piece [0-9a-f]{64}:[0-9]+`)

func dealSecretFile(t testing.TB, name string) string {
	t.Helper()
	raw := make([]byte, dealKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(hex.EncodeToString(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestBackupCLI_dealSlotsOpenToTheBackup: backup --deal-dir seals the backup
// into one slot per replica of a private storage deal, prints the
// 'orama storage create' pieces, and each slot opens with the owner's storage
// key to the same sealed backup, which opens with the backup key to this
// namespace (website/src/docs/operator/run-your-own-cluster.mdx "A backup in a storage deal"). The
// upload to providers and the restore from the deal need a chain and
// providers and are covered where those exist (cli-storage-global).
func TestBackupCLI_dealSlotsOpenToTheBackup(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	k := tenancy.NewBackupKey(t)
	storageKey, repair := dealSecretFile(t, "storage.key"), dealSecretFile(t, "repair.seed")
	nonce := make([]byte, dealKeyBytes)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	nonceHex, dir := hex.EncodeToString(nonce), filepath.Join(t.TempDir(), "slots")

	out := n.CLI.MustOK(t, "namespace", "backup", "--key", k.PubHex, "--deal-dir", dir,
		"--deal-nonce", nonceHex, "--deal-replicas", "2",
		"--storage-key-file", storageKey, "--repair-seed-file", repair).Stdout
	if got := len(pieceFlag.FindAllString(out, -1)); got != dealReplicas {
		t.Fatalf("the output names %d pieces, want %d:\n%s", got, dealReplicas, out)
	}
	if !strings.Contains(out, "--nonce "+nonceHex) {
		t.Fatalf("the printed deal command does not carry the nonce:\n%s", out)
	}

	var plain [][]byte
	for i := range dealReplicas {
		slot := filepath.Join(dir, "slot-"+string(rune('0'+i)))
		opened := filepath.Join(t.TempDir(), "opened")
		n.CLI.MustOK(t, "storage", "open", "--storage-key-file", storageKey, "--repair-seed-file", repair,
			"--nonce", nonceHex, "--slot", string(rune('0'+i)), "--in", slot, "--out", opened)
		blob, err := os.ReadFile(opened)
		if err != nil {
			t.Fatal(err)
		}
		plain = append(plain, blob)
	}
	if !bytes.Equal(plain[0], plain[1]) {
		t.Fatal("the slots hold different backups")
	}
	if p := k.Open(t, plain[0]); p.Namespace != n.Name {
		t.Fatalf("the backup in the slots is of %q, want %q", p.Namespace, n.Name)
	}
}

// TestBackupCLI_dealRefusals: a deal directory without the keys that seal it
// is refused before a backup is requested, a restore names exactly one source,
// and --from-deal needs the chain's RPC. None sends anything to the gateway.
func TestBackupCLI_dealRefusals(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	k := tenancy.NewBackupKey(t)
	dir := t.TempDir()
	dest := cliRestoreKey(t, n.CLI)
	base := []string{"--key-file", k.File(t), "--namespace", n.Name, "--dest-key", dest}
	cases := map[string]struct {
		args []string
		want string
	}{
		"deal-dir without keys": {[]string{"namespace", "backup", "--key", k.PubHex, "--deal-dir", filepath.Join(dir, "s")}, "--deal-nonce"},
		"neither out nor deal":  {[]string{"namespace", "backup", "--key", k.PubHex}, "--out"},
		"in and from-deal":      {append([]string{"namespace", "restore", "--in", "x.orbk", "--from-deal", "1"}, base...), "not both"},
		"no source":             {append([]string{"namespace", "restore"}, base...), "--from-deal"},
		"from-deal without rpc": {append([]string{"namespace", "restore", "--from-deal", "1"}, base...), "--rpc"},
	}
	for name, c := range cases {
		res, err := n.CLI.Run(t.Context(), c.args...)
		if err != nil || res.Exit == 0 || !strings.Contains(res.Stderr+res.Stdout, c.want) {
			t.Errorf("%s: exit %d, err %v, output %q; want a refusal naming %q", name, res.Exit, err, res.Stderr+res.Stdout, c.want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "s")); err == nil {
		t.Error("a refused backup created its deal directory")
	}
}
