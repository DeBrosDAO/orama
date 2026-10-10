package setup

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/install"
)

func seatInstall(user string, services ...install.GlobalService) InitChainInput {
	return InitChainInput{
		Node: NodePlan{IP: ip1, Name: "seed", Profile: install.ProfileFull, Services: services, StorageGB: 50},
		IP:   ip1, User: user, ChainID: "orama-stagenet-6", Contact: "ops@example.org",
	}
}

func TestInitChainCommand_makesTheHomeWithAPlaceholderAndNothingElse(t *testing.T) {
	cmd := InitChainCommand(seatInstall("root", install.GlobalServiceChain, install.GlobalServiceIPFS, install.GlobalServiceProvider))
	want := "sudo /opt/orama/bin/orama global install --colocated --services 'chain,ipfs,provider' --staged-dir /opt/orama/bin " +
		"--manifest /opt/orama/manifest.json --public-storage-gb 50 " +
		"--init-chain --chain-id 'orama-stagenet-6' --moniker 'seed' --genesis '/opt/orama/.setup-global/genesis.json' " +
		"--external-address '203.0.113.11:31000'"
	if cmd != want {
		t.Errorf("init command:\n got %s\nwant %s", cmd, want)
	}
	for _, absent := range []string{"--persistent-peers", "--statesync", "--chain-client-user", "--tor-address"} {
		if strings.Contains(cmd, absent) {
			t.Errorf("the first install of a seat has %q: %s", absent, cmd)
		}
	}
}

func TestWireChainCommand_keepsTheHomeAndSetsThePeers(t *testing.T) {
	in := WireInput{Node: seatInstall("root", install.GlobalServiceChain).Node, IP: ip1, User: "root", Peers: strings.Repeat("a", 40) + "@203.0.113.12:31000"}
	cmd := WireChainCommand(in)
	want := "sudo /opt/orama/bin/orama global install --colocated --services 'chain' --staged-dir /opt/orama/bin " +
		"--manifest /opt/orama/manifest.json --public-storage-gb 50 " +
		"--persistent-peers '" + strings.Repeat("a", 40) + "@203.0.113.12:31000' --external-address '203.0.113.11:31000'"
	if cmd != want {
		t.Errorf("wire command:\n got %s\nwant %s", cmd, want)
	}
	if strings.Contains(cmd, "--init-chain") || strings.Contains(cmd, "--genesis") {
		t.Errorf("a second install must not touch the chain home's keys or genesis: %s", cmd)
	}
	in.Peers = ""
	if strings.Contains(WireChainCommand(in), "--persistent-peers") {
		t.Error("a single seat has no peers to name")
	}
}

func TestCreateInstalls_shareTheJoinsOptions(t *testing.T) {
	services := []install.GlobalService{install.GlobalServiceChain, install.GlobalServiceIPFS, install.GlobalServiceProvider, install.GlobalServiceRelay}
	join := GlobalInstallCommand(joinInstall("ubuntu", services...))
	for name, cmd := range map[string]string{
		"init": InitChainCommand(seatInstall("ubuntu", services...)),
		"wire": WireChainCommand(WireInput{Node: seatInstall("ubuntu", services...).Node, IP: ip1, User: "ubuntu", Contact: "ops@example.org"}),
	} {
		for _, shared := range []string{
			"--services 'chain,ipfs,provider,relay'", "--staged-dir /opt/orama/bin", "--manifest /opt/orama/manifest.json",
			"--public-storage-gb 50", "--external-address '203.0.113.11:31000'", "--chain-client-user 'ubuntu'",
			"--tor-address '203.0.113.11' --tor-contact 'ops@example.org' --tor-node-id 'seed'",
		} {
			if !strings.Contains(cmd, shared) {
				t.Errorf("%s lacks %q:\n%s", name, shared, cmd)
			}
		}
	}
	// The join names its node alice; the options a seat shares with it must be the same words.
	for _, option := range []string{"--colocated", "--staged-dir /opt/orama/bin", "--manifest /opt/orama/manifest.json", "--public-storage-gb 50", "--chain-client-user 'ubuntu'"} {
		if !strings.Contains(join, option) {
			t.Errorf("the join's command no longer has %q: update the seat's installs with it", option)
		}
	}
}

func TestPlaceholderGenesis(t *testing.T) {
	if got := string(placeholderGenesis("orama-stagenet-6")); got != "{\"chain_id\":\"orama-stagenet-6\"}\n" {
		t.Errorf("placeholder = %q", got)
	}
}

func TestSeatScript_makesTheKeyOnlyWhenMissing(t *testing.T) {
	script := seatScript()
	show := "runuser -u orama-chain -- /usr/lib/orama-global/bin/oramad --home /var/lib/orama-global/chain keys show validator --keyring-backend test"
	add := "runuser -u orama-chain -- /usr/lib/orama-global/bin/oramad --home /var/lib/orama-global/chain keys add validator --keyring-backend test --no-backup"
	for _, want := range []string{"if ! " + show + " >/dev/null 2>&1; then", add + " >/dev/null", "comet show-node-id", "comet show-validator", "keys show validator -a --keyring-backend test"} {
		if !strings.Contains(script, want) {
			t.Errorf("seat script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "--unsafe") || strings.Contains(script, "export") {
		t.Errorf("the seat script must never print a private key:\n%s", script)
	}
}

func TestParseSeat_good(t *testing.T) {
	seat := fakeSeat(3)
	out := "__NODE_ID__\n" + seat.NodeID + "\n__CONSENSUS__\n{\"type\":\"tendermint/PubKeyEd25519\",\"key\":\"" + seat.ConsensusPubKey + "\"}\n__SEAT__\n" + seat.Address + "\n"
	got, err := ParseSeat(out)
	if err != nil {
		t.Fatal(err)
	}
	if got != seat {
		t.Errorf("seat = %+v, want %+v", got, seat)
	}
}

func TestParseSeat_refusesWhatItCannotTrust(t *testing.T) {
	seat := fakeSeat(3)
	good := func(id, key, addr string) string {
		return "__NODE_ID__\n" + id + "\n__CONSENSUS__\n{\"key\":\"" + key + "\"}\n__SEAT__\n" + addr + "\n"
	}
	for name, out := range map[string]string{
		"node id not hex":   good("zz"+seat.NodeID[2:], seat.ConsensusPubKey, seat.Address),
		"node id too short": good(seat.NodeID[:38], seat.ConsensusPubKey, seat.Address),
		"key not base64":    good(seat.NodeID, "***", seat.Address),
		"key too short":     good(seat.NodeID, "AAEC", seat.Address),
		"address checksum":  good(seat.NodeID, seat.ConsensusPubKey, seat.Address[:len(seat.Address)-1]+"q"),
		"not an address":    good(seat.NodeID, seat.ConsensusPubKey, "cosmos1abc"),
		"empty":             "",
		"injection":         good(seat.NodeID, seat.ConsensusPubKey, seat.Address+"; rm -rf /"),
	} {
		if _, err := ParseSeat(out); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseHomeState(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	cases := []struct {
		name, out string
		want      HomeState
		fail      bool
	}{
		{"no home", "__GENESIS__\nnone\n__COMMITTEE__\n0\n__STARTED__\n0\n", HomeState{}, false},
		{"placeholder", "__GENESIS__\n" + sum + "\n__COMMITTEE__\n0\n__STARTED__\n0\n", HomeState{Genesis: true, SHA256: sum}, false},
		{"final", "__GENESIS__\n" + sum + "\n__COMMITTEE__\n5\n__STARTED__\n0\n", HomeState{Genesis: true, Final: true, SHA256: sum}, false},
		{"running chain", "__GENESIS__\n" + sum + "\n__COMMITTEE__\n5\n__STARTED__\n1\n", HomeState{Genesis: true, Final: true, SHA256: sum, Started: true}, false},
		{"bad digest", "__GENESIS__\nnope\n__COMMITTEE__\n0\n__STARTED__\n0\n", HomeState{}, true},
		{"bad count", "__GENESIS__\nnone\n__COMMITTEE__\nmany\n__STARTED__\n0\n", HomeState{}, true},
		{"bad started", "__GENESIS__\nnone\n__COMMITTEE__\n0\n__STARTED__\nmaybe\n", HomeState{}, true},
		{"empty", "", HomeState{}, true},
	}
	for _, c := range cases {
		got, err := ParseHomeState(c.out)
		if (err != nil) != c.fail || (!c.fail && got != c.want) {
			t.Errorf("%s: got %+v, %v; want %+v (fail=%v)", c.name, got, err, c.want, c.fail)
		}
	}
}

func TestHomeStateScript_readsAsTheChainAccount(t *testing.T) {
	script := homeStateScript()
	if strings.Contains(script, "sudo") || !strings.Contains(script, "runuser -u orama-chain -- sha256sum /var/lib/orama-global/chain/config/genesis.json") {
		t.Errorf("the chain home is read as its owner, never followed by root:\n%s", script)
	}
	if !strings.Contains(script, "id -u orama-chain") || !strings.Contains(script, "exit 0") {
		t.Errorf("a fresh machine has no chain account yet and no home: the script must say so, not fail:\n%s", script)
	}
	if !strings.Contains(script, `consensus_pubkey`) || !strings.Contains(script, "blockstore.db") {
		t.Errorf("the script must tell a final genesis from the placeholder and a chain that ran:\n%s", script)
	}
}

func TestPutGenesisCommand_writesAsTheChainAccountAndReplacesAtomically(t *testing.T) {
	cmd := putGenesisCommand()
	for _, want := range []string{"runuser -u orama-chain", "mktemp", "mv -f", "umask 077", "/var/lib/orama-global/chain/config"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("put-genesis lacks %q:\n%s", want, cmd)
		}
	}
}

func TestParseEpoch(t *testing.T) {
	if got, err := ParseEpoch(`{"epoch_state":{"current_epoch":"7","genesis_supply":"0"}}`); err != nil || got != 7 {
		t.Errorf("ParseEpoch = %d, %v", got, err)
	}
	for _, bad := range []string{``, `{}`, `{"epoch_state":{"current_epoch":"x"}}`, `<html>`, `{"epoch_state":{"current_epoch":"-1"}}`} {
		if _, err := ParseEpoch(bad); err == nil {
			t.Errorf("ParseEpoch(%q) did not fail", bad)
		}
	}
}

func TestParseChainHealth_distinguishesNoAnswerFromHeightZero(t *testing.T) {
	down := "__STATUS__\n\n__ACTIVE__\nactivating\n__LOG__\nstarting\n"
	h, err := ParseChainHealth(down)
	if err != nil || h.RPCUp || h.Running || h.Height != 0 {
		t.Errorf("no answer: %+v, %v", h, err)
	}
	waiting := "__STATUS__\n{\"result\":{\"sync_info\":{\"latest_block_height\":\"0\",\"catching_up\":false}}}\n__ACTIVE__\nactive\n__LOG__\nwaiting for validators\n"
	h, err = ParseChainHealth(waiting)
	if err != nil || !h.RPCUp || !h.Running || h.Height != 0 {
		t.Errorf("waiting for peers: %+v, %v", h, err)
	}
}
