//go:build e2e_fleet

package opennetworkphases

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/realistic"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestPhaseA6_delegationNamesEveryNameserver: `orama node dns delegation`
// prints the records the parent zone needs, one per nameserver, with each
// nameserver's own address (A6; docs/RUN_YOUR_OWN_CLUSTER.md "Install").
func TestPhaseA6_delegationNamesEveryNameserver(t *testing.T) {
	phase(t, "A6", "docs/RUN_YOUR_OWN_CLUSTER.md", "orama node dns delegation --env", trackA+" A6")
	f := harness.Fleet(t)
	printed := out(harness.CLI(t).MustOK(t, "node", "dns", "delegation", "--env", f.State.Env))
	if !strings.Contains(printed, f.State.BaseDomain) {
		t.Errorf("the delegation does not name %s:\n%s", f.State.BaseDomain, printed)
	}
	for _, n := range tenancy.Nameservers(f) {
		if !strings.Contains(printed, n.PublicIP) {
			t.Errorf("the delegation has no glue for %s (%s):\n%s", n.Name, n.PublicIP, printed)
		}
	}
}

// restoreVisible bounds a restored database reaching every replica.
const restoreVisible = 2 * time.Minute

// deployURL is how `orama deploy` prints the app's address.
var deployURL = regexp.MustCompile(`•\s+(https://\S+)`)

// TestPhaseA7_runYourOwnClusterUseItSteps follows "Use it" of
// docs/RUN_YOUR_OWN_CLUSTER.md literally, on a machine that has the
// environment and nothing else: env use, auth login, namespace create, auth
// login --namespace, deploy static; the site is then served by name (A7).
// The install half of the page runs in provisioning (node setup per node,
// dns delegation), which install and invite-join assert.
func TestPhaseA7_runYourOwnClusterUseItSteps(t *testing.T) {
	phase(t, "A7", "docs/RUN_YOUR_OWN_CLUSTER.md", "orama namespace create myapp", trackA+" A7")
	f := harness.Fleet(t)
	tenancy.Reserve(t, harness.Fleet(t), 1)
	cli := harness.CLI(t).Isolated(t)
	name := ns.UniqueName(t.Name())
	cli.MustOK(t, "env", "use", f.State.Env)
	cli.MustOK(t, "auth", "login")
	cli.MustOK(t, "namespace", "create", name)
	t.Cleanup(func() { deleteCurrent(t, cli, name) })
	waitListed(t, cli, name)
	cli.MustOK(t, "auth", "login", "--namespace", name)
	site := realistic.CopyApp(t, realistic.AppStatic, map[string]string{realistic.ReleaseMarker: "a7-" + name}, nil)
	res := cli.MustOK(t, "deploy", "static", site, "--name", "www")
	m := deployURL.FindStringSubmatch(res.Stdout)
	if m == nil {
		t.Fatalf("orama deploy static printed no URL:\n%s", res.Stdout)
	}
	realistic.Serving(t, harness.GW(t).WithBase(m[1]), "/", "a7-"+name)
}

// A cluster registration's sign document is built with the vector account
// core's own tests pin (core/pkg/clusterreg vectorAddress, vectorPubKey); it
// is printed, never submitted.
const (
	vectorOperator = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	vectorPubKey   = "024f4e2ad99c34d60b9ba6283c9431a8418af8673212961f97a77b6377fcd05b62"
	registerMsg    = "/orama.nodes.v1.MsgRegisterCluster"
	signDocHeader  = "sign document (not submitted):"
)

// TestPhaseA8_clusterRowCarriesNoNodeAddress: registering the cluster on
// chain describes its public name and endpoints and nothing about its
// machines: no node address, public or overlay, is in what would be signed
// (A8; docs/CLI_REFERENCE.md "orama cluster register-onchain").
func TestPhaseA8_clusterRowCarriesNoNodeAddress(t *testing.T) {
	phase(t, "A8", "docs/CLI_REFERENCE.md", "### orama cluster register-onchain", trackA+" A8")
	f := harness.Fleet(t)
	endpoint := "https://" + f.State.BaseDomain
	res := harness.CLI(t).NoWallet(t).MustOK(t, "cluster", "register-onchain", "--id", "e2e-"+f.State.RunID,
		"--base-domain", f.State.BaseDomain, "--endpoint", endpoint, "--operator", vectorOperator, "--pubkey", vectorPubKey,
		"--chain-id", "orama-devnet-e2e-a8", "--fee", "1000", "--gas", "200000", "--account-number", "7", "--sequence", "3")
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) != 2 || lines[0] != signDocHeader {
		t.Fatalf("register-onchain printed %q, want the sign document", res.Stdout)
	}
	doc, err := hex.DecodeString(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{registerMsg, f.State.BaseDomain, endpoint} {
		if !bytes.Contains(doc, []byte(want)) {
			t.Errorf("the sign document lacks %q", want)
		}
	}
	for _, n := range f.AllNodes() {
		for _, addr := range []string{n.PublicIP, n.WGIP} {
			if addr != "" && bytes.Contains(doc, []byte(addr)) {
				t.Errorf("the cluster row carries %s's address %s", n.Name, addr)
			}
		}
	}
}

// TestPhaseA9_sealedBackupRestoresTheDatabase: a backup sealed to a key the
// cluster never holds brings the namespace database back after rows were
// lost: backup, delete, restore-key, restore (A9; docs/RUN_YOUR_OWN_CLUSTER.md
// "A sealed backup"). The global-storage deals and schedule of the plan's A9
// are not in this release ("there is no schedule and no storage deal").
func TestPhaseA9_sealedBackupRestoresTheDatabase(t *testing.T) {
	phase(t, "A9", "docs/RUN_YOUR_OWN_CLUSTER.md", "orama namespace backup --key", trackA+" A9")
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{Via: ns.ViaOperator})
	db := newDB(t, f, n)
	db.rows(t, "kept-1", "kept-2", "kept-3")
	pub, keyFile := backupKey(t)
	file := filepath.Join(t.TempDir(), "ns.orbk")
	n.CLI.MustOK(t, "namespace", "backup", "--key", pub, "--out", file)
	db.exec(t, "DELETE FROM a9 WHERE v != ?", "kept-1")
	if got := db.count(t); got != 1 {
		t.Fatalf("after the delete %d rows remain, want 1", got)
	}
	dest := strings.TrimSpace(n.CLI.MustOK(t, "namespace", "restore-key").Stdout)
	n.CLI.MustOK(t, "namespace", "restore", "--in", file, "--key-file", keyFile, "--namespace", n.Name, "--dest-key", lastHexLine(dest))
	eventually.Require(t, pollEvery, restoreVisible, "the backup's 3 rows after the restore", func() (bool, error) {
		got := db.count(t)
		if got == 3 {
			return true, nil
		}
		return false, fmt.Errorf("%d rows", got)
	})
}

// backupKey is an X25519 keypair the way `orama namespace restore --key-file`
// reads it: the public key hex for backup, the private key hex in a 0600 file.
func backupKey(t *testing.T) (string, string) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(k.Bytes())), 0o600); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(k.PublicKey().Bytes()), path
}

var hexKeyLine = regexp.MustCompile(`(?m)^[0-9a-f]{64}$`)

func lastHexLine(s string) string {
	all := hexKeyLine.FindAllString(s, -1)
	if len(all) == 0 {
		return s
	}
	return all[len(all)-1]
}
