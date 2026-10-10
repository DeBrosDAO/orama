package setup

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/statesync"
)

func joinInstall(user string, services ...install.GlobalService) GlobalInstall {
	return GlobalInstall{
		Node: NodePlan{IP: ip1, Name: "alice", Profile: install.ProfileFull, Services: services, StorageGB: 50},
		IP:   ip1, User: user, ChainID: testChainID, Contact: "ops@example.org",
		Trust: &statesync.TrustPoint{
			Height: 4900, Hash: testSeedHash,
			Servers: []string{"https://seed1.stagenet.example/v1/chain/light", "https://seed2.stagenet.example/v1/chain/light"},
			Peers:   []statesync.Peer{{NodeID: strings.Repeat("a", 40), Host: "seed1.stagenet.example"}, {NodeID: strings.Repeat("b", 40), Host: "seed2.stagenet.example"}},
		},
	}
}

func TestGlobalInstallCommand_chainJoinedByStateSync(t *testing.T) {
	cmd := GlobalInstallCommand(joinInstall("root", install.GlobalServiceChain, install.GlobalServiceIPFS, install.GlobalServiceProvider))
	for _, want := range []string{
		"sudo /opt/orama/bin/orama global install --colocated", "--services 'chain,ipfs,provider'",
		"--staged-dir /opt/orama/bin", "--manifest /opt/orama/manifest.json", "--public-storage-gb 50",
		"--init-chain --chain-id 'orama-stagenet-6' --moniker 'alice' --genesis '/opt/orama/.setup-global/genesis.json'",
		"--persistent-peers '" + strings.Repeat("a", 40) + "@seed1.stagenet.example:31000," + strings.Repeat("b", 40) + "@seed2.stagenet.example:31000'",
		"--external-address '203.0.113.11:31000'", "--statesync-trust-height 4900", "--statesync-trust-hash '" + testSeedHash + "'",
		"--statesync-rpc 'https://seed1.stagenet.example/v1/chain/light' --statesync-rpc 'https://seed2.stagenet.example/v1/chain/light'",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q:\n%s", want, cmd)
		}
	}
	for _, absent := range []string{"--chain-client-user", "--tor-address"} {
		if strings.Contains(cmd, absent) {
			t.Errorf("command has %q though neither applies:\n%s", absent, cmd)
		}
	}
}

func TestGlobalInstallCommand_nonRootLoginMayReachTheChain(t *testing.T) {
	cmd := GlobalInstallCommand(joinInstall("ubuntu", install.GlobalServiceChain))
	if !strings.Contains(cmd, "--chain-client-user 'ubuntu'") {
		t.Errorf("the ssh login that runs `orama chain` is an allowed chain client:\n%s", cmd)
	}
}

func TestGlobalInstallCommand_relayCarriesTheTorOptions(t *testing.T) {
	in := joinInstall("root", install.GlobalServiceChain, install.GlobalServiceRelay)
	in.Node.Exit = true
	cmd := GlobalInstallCommand(in)
	for _, want := range []string{"--services 'chain,relay,exit'", "--tor-address '203.0.113.11'", "--tor-contact 'ops@example.org'", "--tor-node-id 'alice'"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command lacks %q:\n%s", want, cmd)
		}
	}
}

func TestGlobalInstallCommand_aValueWithAQuoteStaysInsideItsQuotes(t *testing.T) {
	in := joinInstall("root", install.GlobalServiceChain, install.GlobalServiceRelay)
	in.Contact = `x'; rm -rf / #`
	cmd := GlobalInstallCommand(in)
	if !strings.Contains(cmd, `--tor-contact 'x'"'"'; rm -rf / #'`) {
		t.Errorf("the contact is not quoted:\n%s", cmd)
	}
}

func TestStartGlobalCommand(t *testing.T) {
	plain := NodePlan{Services: []install.GlobalService{install.GlobalServiceChain, install.GlobalServiceIPFS, install.GlobalServiceProvider}}
	if got := StartGlobalCommand(plain); got != "sudo /opt/orama/bin/orama global start chain ipfs" {
		t.Errorf("got %q: the provider waits for its node id", got)
	}
	relay := NodePlan{Services: append(plain.Services, install.GlobalServiceRelay)}
	if got := StartGlobalCommand(relay); !strings.HasSuffix(got, "chain ipfs relay") {
		t.Errorf("got %q", got)
	}
}

const rpcStatus = `{"jsonrpc":"2.0","id":-1,"result":{"sync_info":{"latest_block_height":"5123","catching_up":false}}}`

func TestParseChainState(t *testing.T) {
	out := "__STATUS__\n" + rpcStatus + "\n\n__ACTIVE__\nactive\n__LOG__\nlast line\n"
	st, err := ParseChainState(out)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Height != 5123 || st.CatchingUp || st.Detail != "last line" {
		t.Fatalf("%+v", st)
	}
}

func TestParseChainState_rpcNotUpYet(t *testing.T) {
	st, err := ParseChainState("__STATUS__\n\n__ACTIVE__\nactivating\n__LOG__\nstarting\n")
	if err != nil || st.Running || st.Height != 0 {
		t.Fatalf("%+v, %v: a unit still activating has no height and is not running", st, err)
	}
	st, err = ParseChainState("__STATUS__\n\n__ACTIVE__\nactive\n__LOG__\n")
	if err != nil || !st.Running || st.Height != 0 {
		t.Fatalf("%+v, %v: a running unit whose RPC is not open yet is waited for", st, err)
	}
}

func TestParseChainState_catchingUp(t *testing.T) {
	out := "__STATUS__\n" + strings.Replace(rpcStatus, `"catching_up":false`, `"catching_up":true`, 1) + "\n__ACTIVE__\nactive\n__LOG__\n"
	if st, err := ParseChainState(out); err != nil || !st.CatchingUp {
		t.Fatalf("%+v, %v", st, err)
	}
}

func TestParseChainState_garbage(t *testing.T) {
	if _, err := ParseChainState("__STATUS__\nnot json\n__ACTIVE__\nactive\n"); err == nil {
		t.Error("a status that is not JSON is an error")
	}
	if _, err := ParseChainState("__STATUS__\n" + strings.Replace(rpcStatus, "5123", "many", 1) + "\n__ACTIVE__\nactive\n"); err == nil {
		t.Error("a height that is not a number is an error")
	}
}

// secp256k1 public key of the standard test vector (private key 1).
const testPub = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"

func identityOutput(t *testing.T, nodeID string, consensus []byte, mutate func(map[string]string)) string {
	t.Helper()
	binding := map[string]string{"service": "hot-key", "key_type": "secp256k1", "pubkey": testPub, "signature": hex.EncodeToString(make([]byte, 64))}
	if mutate != nil {
		mutate(binding)
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := json.Marshal(map[string]string{"@type": "/cosmos.crypto.ed25519.PubKey", "key": base64.StdEncoding.EncodeToString(consensus)})
	return "__NODE_ID__\n" + nodeID + "\n__CONSENSUS__\n" + string(key) + "\n__BINDING__\n" + string(raw) + "\n"
}

func TestParseIdentity(t *testing.T) {
	consensus := make([]byte, 32)
	consensus[0] = 7
	id, err := ParseIdentity(identityOutput(t, strings.Repeat("ab", 20), consensus, nil))
	if err != nil {
		t.Fatal(err)
	}
	if id.ChainNodeID != strings.Repeat("ab", 20) || id.ConsensusPubKey[0] != 7 {
		t.Errorf("%+v", id)
	}
	// The hot key's address is derived from the public key the binding carries.
	if !strings.HasPrefix(id.HotKey, "orama1") || id.HotBinding.Service != "hot-key" || hex.EncodeToString(id.HotBinding.Pubkey) != testPub {
		t.Errorf("%+v", id)
	}
}

func TestParseIdentity_refusals(t *testing.T) {
	consensus := make([]byte, 32)
	good := strings.Repeat("ab", 20)
	for name, out := range map[string]string{
		"a short node id":       identityOutput(t, "abcd", consensus, nil),
		"a short consensus key": identityOutput(t, good, make([]byte, 16), nil),
		"a binding for another service": identityOutput(t, good, consensus, func(b map[string]string) {
			b["service"] = "provider"
		}),
		"a pubkey that is not hex":    identityOutput(t, good, consensus, func(b map[string]string) { b["pubkey"] = "zz" }),
		"a signature that is not hex": identityOutput(t, good, consensus, func(b map[string]string) { b["signature"] = "zz" }),
		"nothing":                     "",
	} {
		if _, err := ParseIdentity(out); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestIdentityScript_neverPrintsAPrivateKey(t *testing.T) {
	script := identityScript(IdentityRequest{ChainID: testChainID, Operator: testOperator})
	for _, want := range []string{"comet show-node-id", "comet show-validator", "global bind --chain-id 'orama-stagenet-6' --operator '" + testOperator + "' --service hot-key", "--key-type secp256k1", "2>/dev/null"} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(script, "cat \"$HK\"") || strings.Contains(script, "echo \"$HK\"") {
		t.Error("the hot key's contents must never be printed")
	}
}

func TestStartServicesScript_takesTheNodeIDFromStdin(t *testing.T) {
	script := startServicesScript()
	if !strings.Contains(script, "id=$(cat)") || !strings.Contains(script, "global start provider") || !strings.Contains(script, "node-id") {
		t.Errorf("script:\n%s", script)
	}
}

func TestGlobalStageCommands(t *testing.T) {
	if cmd := stageGlobalDirCommand(); !strings.Contains(cmd, "install -d -m 0700 -o root -g root /opt/orama/.setup-global") {
		t.Errorf("the genesis directory must be root's alone: %s", cmd)
	}
	if cmd := writeGenesisCommand(); !strings.Contains(cmd, "umask 077") || !strings.Contains(cmd, "/opt/orama/.setup-global/genesis.json") {
		t.Errorf("genesis write: %s", cmd)
	}
	if cmd := writeTorNetworkCommand(); !strings.Contains(cmd, "/opt/orama/bin/tor-network.json") {
		t.Errorf("tor network write: %s", cmd)
	}
	if removeGlobalStageCommand() != "sudo rm -rf /opt/orama/.setup-global" {
		t.Error("cleanup")
	}
}
