//go:build e2e_fleet

package joinonecommand

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/infra"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/netregistry"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

const (
	// torNetworkEnv, set to 1, makes TestSetup_createANetworkWithItsOwnTorNetwork run. The
	// step it checks takes two and a half hours, which is longer than the ops stage's budget
	// (e2e/stages/stages.yaml), so a run that sets it widens that stage.
	torNetworkEnv = "E2E_SETUP_TOR_NETWORK"
	// torCreateBudget bounds the creation with its Tor network: the chain, then about an hour
	// for the first consensus and 30 minutes for each authority after the first.
	torCreateBudget = 4 * time.Hour
	// authorityKeysHome is where the authorities' keys are made on their machines.
	authorityKeysHome = constants.GlobalTorAuthorityKeysDir
)

// TestSetup_createANetworkWithItsOwnTorNetwork: `orama setup --create-network` on three fresh
// servers, with the default of three directory authorities, makes the network's own Tor
// network after the chain runs: the authorities' keys on their own machines, the final
// tor-network.json pinned in the manifest, a consensus signed by a majority of the
// authorities that lists every machine, and a second run that changes nothing.
func TestSetup_createANetworkWithItsOwnTorNetwork(t *testing.T) {
	if os.Getenv(torNetworkEnv) != "1" {
		harness.SkipNotApplicable(t, "set "+torNetworkEnv+"=1 (and widen the stage budget): the network's own Tor network takes two and a half hours after the chain is up")
	}
	f := harness.Fleet(t)
	cli := isolatedCLI(t)
	extras := []harness.Extra{
		harness.ExtraNode(t, "join-one-tor-1", extraLocation),
		harness.ExtraNode(t, "join-one-tor-2", extraLocation),
		harness.ExtraNode(t, "join-one-tor-3", extraLocation),
	}
	publishDir := filepath.Join(t.TempDir(), "networks")
	args := createArgsWith(t, publishDir, nil, extras...)
	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"threeFreshServersMakeTheirOwnTorNetwork", func(t *testing.T) {
			res := infra.RunFor(t, cli, torCreateBudget, args...)
			infra.ExpectExit(t, res, infra.ExitOK, "Done.", "the Tor network "+createdNetwork+" is up: 3 directory authorities and 0 relays")
			published, network := publishedTorNetwork(t, publishDir)
			for i, e := range extras {
				// The authorities are nicknamed in the order of the machines.
				requireAuthority(t, f, e, "OramaAuth0"+string(rune('1'+i)), published, network)
			}
		}},
		{"theSameCommandAgainChangesNothing", func(t *testing.T) {
			res := infra.RunFor(t, cli, torCreateBudget, args...)
			infra.ExpectExit(t, res, infra.ExitOK, "already runs the network file without the bootstrap mode")
			if strings.Contains(res.Stdout, "restarting the") || strings.Contains(res.Stdout, "installing the") {
				t.Errorf("the second run touched a Tor role:\n%s", res.Stdout)
			}
			_, _ = publishedTorNetwork(t, publishDir)
		}},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) {
			t.Fatalf("step %s failed; the later steps depend on it", s.name)
		}
	}
}

// publishedTorNetwork reads networks/<name>/ and checks the manifest pins the Tor network file
// beside it, which is the final one (without the bootstrap mode) with three authorities.
func publishedTorNetwork(t *testing.T, publishDir string) ([]byte, tornet.Network) {
	t.Helper()
	dir := filepath.Join(publishDir, createdNetwork)
	raw, err := os.ReadFile(filepath.Join(dir, netregistry.ManifestFile))
	if err != nil {
		t.Fatalf("the creation wrote no manifest: %v", err)
	}
	m, err := netregistry.ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(dir, netregistry.TorNetworkFile))
	if err != nil {
		t.Fatalf("the creation wrote no tor-network.json: %v", err)
	}
	if err := m.VerifyTorNetwork(file); err != nil {
		t.Errorf("the manifest does not pin the Tor network file written: %v", err)
	}
	n, err := tornet.ParseNetwork(file)
	if err != nil {
		t.Fatal(err)
	}
	if n.Bootstrap || len(n.Authorities) != createdSeats || len(n.ValidatorOnions) != 0 || n.Name != createdNetwork {
		t.Errorf("tor-network.json = %+v, want the final file of %s with %d authorities", n, createdNetwork, createdSeats)
	}
	return file, n
}

// requireAuthority checks one authority machine: the network file it runs is the pinned one,
// its identity key is only in the keys home (encrypted, read-only for root, with its
// passphrase) and never in tor's DataDirectory, and the consensus it holds is signed by a
// majority of the authorities and lists it.
func requireAuthority(t *testing.T, f *fleet.Fleet, e harness.Extra, nick string, published []byte, network tornet.Network) {
	t.Helper()
	out := f.Exec(t, e.Node, "sha256sum "+constants.GlobalStateRoot+"/"+constants.GlobalTorAuthoritiesFile)
	if want := netregistry.Digest(published); !strings.HasPrefix(out.Stdout, want) {
		t.Errorf("%s runs the network file %q, want the pinned one (sha256 %s)", e.PublicIP, strings.TrimSpace(out.Stdout), want)
	}
	home := authorityKeysHome + "/offline/" + nick
	infra.RequireStat(t, f, e.Node, home+"/"+tornet.KeyAuthorityIdentity, "root", "root", "400")
	infra.RequireStat(t, f, e.Node, home+"/"+tornet.AuthorityPassphraseFile, "root", "root", "400")
	if _, ok := infra.StatFile(t, f, e.Node, constants.GlobalTorDirauthHome+"/keys/"+tornet.KeyAuthorityIdentity); ok {
		t.Errorf("%s: the authority identity key is in tor's DataDirectory", e.PublicIP)
	}
	info := infra.OnNode(t, f, e.Node, "global", "tor", "info", "--json")
	infra.ExpectNodeExit(t, "orama global tor info", info, infra.ExitOK)
	var roles []tornet.NodeInfo
	if err := json.Unmarshal([]byte(info.Stdout), &roles); err != nil {
		t.Fatalf("%s: orama global tor info --json is not JSON: %v\n%s", e.PublicIP, err, info.Stdout)
	}
	for _, r := range roles {
		if r.Home != constants.GlobalTorDirauthHome {
			continue
		}
		c := r.Consensus
		if c == nil || !c.Valid || c.Signatures < len(network.Authorities)/2+1 || !c.Listed {
			t.Errorf("%s: the consensus the authority holds is %+v, want a valid one signed by a majority that lists it", e.PublicIP, c)
		}
		return
	}
	t.Errorf("%s: orama global tor info lists no directory authority:\n%s", e.PublicIP, info.Stdout)
}

// TestSetup_torAuthoritiesIsRefusedWhenItCannotWork: --tor-authorities is refused, with the
// reason, before any server is contacted: fewer than three, more than the machines, together
// with another Tor network or with --no-relay, a negative number, machines in a reserved
// range (the default of three authorities included), and without --create-network.
func TestSetup_torAuthoritiesIsRefusedWhenItCannotWork(t *testing.T) {
	cli := isolatedCLI(t)
	machines := []string{
		"--ip", unusedIP, "--host-key", unusedIP + "=SHA256:abc",
		"--ip", "203.0.113.78", "--host-key", "203.0.113.78=SHA256:abc",
		"--ip", "203.0.113.79", "--host-key", "203.0.113.79=SHA256:abc",
	}
	create := []string{"setup", "--yes", "--create-network", "e2etor", "--chain-id", "orama-e2etor-devnet-1", "--release-root", "release-root.json"}
	cases := []struct {
		name  string
		extra []string
		want  []string
	}{
		{"fewerThanThree", []string{"--tor-authorities", "2"}, []string{"at least 3 directory authorities", "majority"}},
		{"moreThanTheMachines", []string{"--tor-authorities", "4"}, []string{"needs 4 machines", "has 3"}},
		{"withAnotherTorNetwork", []string{"--tor-authorities", "3", "--tor-network", "tor-network.json"}, []string{"--tor-network", "pass one of them"}},
		{"withNoRelay", []string{"--tor-authorities", "3", "--no-relay"}, []string{"--no-relay", "--tor-authorities 0"}},
		{"negative", []string{"--tor-authorities=-1"}, []string{"give 0 to run no Tor network"}},
		{"theDefaultOnReservedAddresses", nil, []string{"cannot be in a Tor network", "--tor-authorities 0"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := append(append(append([]string(nil), create...), machines...), c.extra...)
			infra.ExpectExit(t, infra.Run(t, cli, args...), infra.ExitUsage, c.want...)
		})
	}
	t.Run("withoutCreateNetwork", func(t *testing.T) {
		args := append([]string{"setup", "--yes", "--tor-authorities", "3", "--name", "alice"}, machines...)
		infra.ExpectExit(t, infra.Run(t, cli, args...), infra.ExitUsage, "--tor-authorities", "--create-network")
	})
}
