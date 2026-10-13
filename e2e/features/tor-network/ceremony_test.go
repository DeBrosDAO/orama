//go:build e2e_fleet

package tornetwork

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const ceremonyPassphrase = "e2e ceremony passphrase, not a secret"

// e2eOnion is a well-formed validator onion address (56 base32 characters);
// the ceremony test only writes it into a file.
const e2eOnion = "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"

// publicLooking are three public addresses the ceremony writes into its
// network file. Nothing connects to them.
var publicLooking = []string{"192.5.5.241", "198.41.0.4", "199.7.91.13"}

// ceremonyDir makes a private work directory on n and removes it after the test.
func ceremonyDir(t *testing.T, f *fleet.Fleet, n fleet.Node) string {
	t.Helper()
	dir := "/tmp/e2e-tornet-ceremony-" + randomSuffix(t)
	f.MustExec(t, n, "install -d -m 0700 "+dir)
	t.Cleanup(func() { cleanup(t, f, n, "rm -rf "+dir) })
	return dir
}

// ceremonyArgs is `orama maint global tor ceremony` into out with the given authorities.
func ceremonyArgs(out, passFile string, authorities ...string) []string {
	args := []string{"maint", "global", "tor", "ceremony", "--name", "orama-e2e", "--out", out, "--passphrase-file", passFile}
	for _, a := range authorities {
		args = append(args, "--authority", a)
	}
	return args
}

// threeAuthorities are NICKNAME=IP pairs on the public-looking addresses.
func threeAuthorities() []string {
	var out []string
	for i, ip := range publicLooking {
		out = append(out, "E2EAuth"+string(rune('A'+i))+"="+ip)
	}
	return out
}

// TestCeremony_realTorMakesKeysTheNetworkFileNames: `orama global tor
// ceremony` runs the node's own upstream tor and tor-gencert. The network file
// it writes loads, names three distinct authorities, and the keys are split as
// documented: the identity key only under offline/, never in a deploy bundle,
// and no output carries the passphrase.
func TestCeremony_realTorMakesKeysTheNetworkFileNames(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	if out := f.Exec(t, n, "command -v tor-gencert"); out.Exit != 0 {
		harness.SkipNotApplicable(t, "tor-gencert is not installed on "+n.Name+": the tor package of the Tor Project's repository ships it")
	}
	dir := ceremonyDir(t, f, n)
	pass := dir + "/passphrase"
	f.MustExec(t, n, "umask 077 && printf %s "+fleet.ShellQuote(ceremonyPassphrase)+" > "+pass)
	out := infra.OnNode(t, f, n, ceremonyArgs(dir+"/out", pass, threeAuthorities()...)...)
	infra.ExpectNodeExit(t, "the ceremony", out, infra.ExitOK, "Move "+dir+"/out/offline to offline media")
	if strings.Contains(out.Stdout+out.Stderr, ceremonyPassphrase) {
		t.Error("the passphrase is in the ceremony's output")
	}
	network, err := tornet.ParseNetwork(f.ReadFile(t, n, dir+"/out/"+constants.TorNetworkFile))
	if err != nil {
		t.Fatalf("the ceremony's network file: %v", err)
	}
	if network.Name != "orama-e2e" || !network.Private || len(network.Authorities) != len(publicLooking) || !network.Bootstrap || network.AllowExit || len(network.ValidatorOnions) != 0 {
		t.Fatalf("network = %+v", network)
	}
	for _, a := range network.Authorities {
		offline := dir + "/out/" + tornet.CeremonyOfflineDir + "/" + a.Nickname + "/" + tornet.KeyAuthorityIdentity
		deploy := dir + "/out/" + tornet.CeremonyDeployDir + "/" + a.Nickname + "/keys/"
		if st, ok := infra.StatFile(t, f, n, offline); !ok || st.Mode != fmt.Sprintf("%o", tornet.CeremonyOfflineKeyMode) {
			t.Errorf("%s: the identity key under offline/ is %+v (present %v), want mode %o", a.Nickname, st, ok, tornet.CeremonyOfflineKeyMode)
		}
		if _, ok := infra.StatFile(t, f, n, deploy+tornet.KeyAuthorityIdentity); ok {
			t.Errorf("%s: the identity key is in the deploy bundle", a.Nickname)
		}
		for _, k := range []string{tornet.KeyAuthoritySigning, tornet.KeyAuthorityCert, tornet.KeyRelayIdentity, tornet.KeyEd25519Master} {
			if _, ok := infra.StatFile(t, f, n, deploy+k); !ok {
				t.Errorf("%s: the deploy bundle lacks %s", a.Nickname, k)
			}
		}
		v3, expires, err := tornet.ParseAuthorityCertificate(string(f.ReadFile(t, n, deploy+tornet.KeyAuthorityCert)))
		if err != nil || v3 != a.V3Ident {
			t.Errorf("%s: certificate identity %s (%v), network file says %s", a.Nickname, v3, err, a.V3Ident)
		}
		if months := time.Until(expires).Hours() / 24 / 30; months < tornet.CertMonths-1 || months > tornet.CertMonths+1 {
			t.Errorf("%s: the signing certificate expires in about %.1f months, want %d", a.Nickname, months, tornet.CertMonths)
		}
		fp, err := tornet.RelayFingerprint(f.ReadFile(t, n, deploy+tornet.KeyRelayIdentity))
		if err != nil || fp != a.Fingerprint {
			t.Errorf("%s: relay identity key hashes to %s (%v), network file says %s", a.Nickname, fp, err, a.Fingerprint)
		}
	}
	// The validator onion addresses exist only after the ceremony; the operator
	// adds them to the same file, which still loads, with nothing else changed.
	file := dir + "/out/" + constants.TorNetworkFile
	added := infra.OnNode(t, f, n, "maint", "global", "tor", "onions", "add", "--network-file", file, e2eOnion)
	infra.ExpectNodeExit(t, "adding a validator onion to the network file", added, infra.ExitOK, "1 added")
	withOnion, err := tornet.ParseNetwork(f.ReadFile(t, n, file))
	if err != nil || len(withOnion.ValidatorOnions) != 1 || withOnion.ValidatorOnions[0] != e2eOnion || len(withOnion.Authorities) != len(network.Authorities) {
		t.Fatalf("the network file after the add: %+v, %v", withOnion, err)
	}
	refused := infra.OnNode(t, f, n, "maint", "global", "tor", "onions", "add", "--network-file", file, "chain.example.com")
	infra.ExpectNodeExit(t, "adding a clearnet host as a validator onion", refused, infra.ExitUsage, "onion")
	// A second ceremony never writes over the first one's keys.
	again := infra.OnNode(t, f, n, ceremonyArgs(dir+"/out", pass, threeAuthorities()...)...)
	infra.ExpectNodeExit(t, "a ceremony into a directory that holds keys", again, infra.ExitFailure, "never writes over keys")
}

// TestCeremony_refusals: the ceremony refuses, before it runs tor, fewer than
// three authorities, a passphrase file others can read, a passphrase that is
// too short, and an authority on a private address.
func TestCeremony_refusals(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := f.State.Nodes[0]
	dir := ceremonyDir(t, f, n)
	good, loose, short := dir+"/good", dir+"/loose", dir+"/short"
	f.MustExec(t, n, "umask 077 && printf %s "+fleet.ShellQuote(ceremonyPassphrase)+" > "+good+" && printf short > "+short)
	f.MustExec(t, n, "printf %s "+fleet.ShellQuote(ceremonyPassphrase)+" > "+loose+" && chmod 0644 "+loose)
	three := threeAuthorities()
	cases := []struct {
		name string
		args []string
		exit int
		want string
	}{
		{"two authorities", ceremonyArgs(dir+"/a", good, three[:2]...), infra.ExitFailure, "at least 3 authorities"},
		{"a passphrase file others can read", ceremonyArgs(dir+"/b", loose, three...), infra.ExitUsage, "mode 0600"},
		{"a passphrase that is too short", ceremonyArgs(dir+"/c", short, three...), infra.ExitFailure, "at least 16 characters"},
		{"an authority on a private address", ceremonyArgs(dir+"/d", good, "A=10.0.0.1", three[1], three[2]), infra.ExitFailure, "not a public address"},
		{"an authority that is not NICKNAME=IP", ceremonyArgs(dir+"/e", good, "nonsense"), infra.ExitUsage, "NICKNAME=IPv4"},
	}
	for _, c := range cases {
		infra.ExpectNodeExit(t, c.name, infra.OnNode(t, f, n, c.args...), c.exit, c.want)
		if _, ok := infra.StatFile(t, f, n, c.args[6]); ok {
			t.Errorf("%s: a refused ceremony wrote output", c.name)
		}
	}
}
