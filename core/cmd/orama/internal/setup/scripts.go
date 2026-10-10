package setup

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/clusterops"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/globalbind"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// The commands setup runs on a node. They are built here, as strings with
// every value quoted, so the tests read exactly what reaches the machine.

const (
	stagedBinDir = "/opt/orama/bin"
	orama        = stagedBinDir + "/orama"
	// providerAccount is the storage provider's system account.
	providerAccount = "orama-provider"
	// globalStageDir is where the genesis is put for `orama global install
	// --init-chain`: a root-only directory, because the installer refuses a genesis
	// whose directory anyone else could write.
	globalStageDir = "/opt/orama/.setup-global"
	// torNetworkPath is where the Tor network file goes: beside the staged
	// binaries, where `orama global install` reads it.
	torNetworkPath = stagedBinDir + "/" + constants.TorNetworkFile
	// markers split the identity script's output.
	markNodeID    = "__NODE_ID__"
	markConsensus = "__CONSENSUS__"
	markBinding   = "__BINDING__"
	// markConsensusBinding precedes the consensus key's binding, printed only
	// when the request asks for it.
	markConsensusBinding = "__CONSENSUS_BINDING__"
	markStatus           = "__STATUS__"
	markActive           = "__ACTIVE__"
	markLog              = "__LOG__"
	// hotKeyWaitSeconds bounds the wait for the provider to create its hot key.
	hotKeyWaitSeconds = 60
	// chainLogLines is how much of the chain's log an error carries.
	chainLogLines = 15
)

var chainRPCStatus = fmt.Sprintf("http://%s:%d/status", constants.GlobalNetnsAddr, constants.ChainRPCPort)

// bash wraps a script so a node whose login shell is not bash still runs it.
func bash(sudo, script string) string { return sudo + "bash -c " + clusterops.ShellQuote(script) }

// GlobalInstallCommand is `orama global install` for a node of the plan: the
// services, the chain joined by state sync, the external address, and, for a
// relay, the Tor options. Every value is single-quoted; the command runs as root.
func GlobalInstallCommand(in GlobalInstall) string {
	n, q := in.Node, clusterops.ShellQuote
	parts := []string{"sudo", orama, "global", "install", "--colocated",
		"--services", q(strings.Join(n.ServiceNames(), ",")),
		"--staged-dir", stagedBinDir, "--manifest", install.DefaultStagedManifest,
		"--public-storage-gb", strconv.FormatUint(n.StorageGB, 10),
		"--init-chain", "--chain-id", q(in.ChainID), "--moniker", q(n.Name), "--genesis", q(globalStageDir + "/genesis.json"),
		"--persistent-peers", q(in.Trust.PersistentPeers()),
		"--external-address", q(fmt.Sprintf("%s:%d", in.IP, constants.ChainP2PPort)),
		"--statesync-trust-height", strconv.FormatInt(in.Trust.Height, 10),
		"--statesync-trust-hash", q(in.Trust.Hash),
	}
	for _, server := range in.Trust.Servers {
		parts = append(parts, "--statesync-rpc", q(server))
	}
	if in.User != DefaultSSHUser {
		parts = append(parts, "--chain-client-user", q(in.User))
	}
	if n.HasService(install.GlobalServiceRelay) {
		parts = append(parts, "--tor-address", q(in.IP), "--tor-contact", q(in.Contact), "--tor-node-id", q(n.Name))
	}
	return strings.Join(parts, " ")
}

// StartGlobalCommand starts the services that do not need the node registered:
// the chain, the public IPFS and the relay. The provider is started after the
// registration.
func StartGlobalCommand(n NodePlan) string {
	services := []string{"chain", "ipfs"}
	if n.HasService(install.GlobalServiceRelay) {
		services = append(services, "relay")
	}
	return "sudo " + orama + " global start " + strings.Join(services, " ")
}

// StageGlobalScript makes the root-only directory the genesis goes into, empty.
func stageGlobalDirCommand() string {
	return bash("sudo ", fmt.Sprintf("set -eu; rm -rf %[1]s; install -d -m 0700 -o root -g root %[1]s", globalStageDir))
}

func writeGenesisCommand() string {
	return bash("sudo ", fmt.Sprintf("set -eu; umask 077; cat > %s/genesis.json", globalStageDir))
}

func writeTorNetworkCommand() string {
	return bash("sudo ", fmt.Sprintf("set -eu; umask 022; cat > %s", torNetworkPath))
}

func removeGlobalStageCommand() string { return "sudo rm -rf " + globalStageDir }

// ChainStateScript prints the chain's RPC status, whether its unit is active,
// and the end of its log.
func chainStateScript() string {
	return fmt.Sprintf(`echo %s; curl -fsS --max-time 10 %s || true; echo
echo %s; systemctl is-active %s || true
echo %s; journalctl -u %s -n %d --no-pager 2>&1 | tail -n %d || true`,
		markStatus, chainRPCStatus, markActive, constants.ChainServiceUnit, markLog, constants.ChainServiceUnit, chainLogLines, chainLogLines)
}

// ParseChainState reads chainStateScript's output. An RPC that has not answered
// yet is a running chain at height zero, not an error: a joining node takes a
// moment to open it.
func ParseChainState(out string) (ChainState, error) {
	sec := splitMarked(out, markStatus, markActive, markLog)
	st := ChainState{Running: strings.TrimSpace(sec[markActive]) == "active", Detail: CleanTerminal(strings.TrimSpace(sec[markLog]))}
	body := strings.TrimSpace(sec[markStatus])
	if body == "" {
		return st, nil
	}
	var doc struct {
		Result struct {
			SyncInfo struct {
				Height     string `json:"latest_block_height"`
				CatchingUp bool   `json:"catching_up"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return ChainState{}, fmt.Errorf("the chain's status is not JSON: %w (%.80q)", err, body)
	}
	height, err := strconv.ParseInt(doc.Result.SyncInfo.Height, 10, 64)
	if err != nil {
		return ChainState{}, fmt.Errorf("the chain's latest_block_height %q is not a number", doc.Result.SyncInfo.Height)
	}
	st.Height, st.CatchingUp = height, doc.Result.SyncInfo.CatchingUp
	return st, nil
}

// splitMarked splits out at the marker lines: marker -> the text after it.
func splitMarked(out string, marks ...string) map[string]string {
	sec := map[string]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		matched := false
		for _, m := range marks {
			if strings.TrimSpace(line) == m {
				cur, matched = m, true
				break
			}
		}
		if !matched && cur != "" {
			sec[cur] += line + "\n"
		}
	}
	return sec
}

// IdentityScript, as root: makes the node's hot key if it has none (the provider
// creates it on its first start, then stops for want of a node id), and prints
// the chain node id, the consensus public key, and the hot key's binding signed
// for the operator on this chain. With in.BindConsensus it also prints the
// consensus key's binding, signed here by the key CometBFT signs blocks with: that
// key never leaves the machine. No private key is printed.
func identityScript(in IdentityRequest) string {
	oramad := constants.GlobalBinDir + "/" + constants.ChainDaemonName
	script := strings.NewReplacer(
		"{PROVIDER}", constants.GlobalProviderHome, "{CHAINUSER}", constants.ChainUser, "{ORAMAD}", oramad,
		"{CHAINHOME}", constants.ChainHome, "{CLI}", constants.GlobalBinDir+"/orama", "{WAIT}", strconv.Itoa(hotKeyWaitSeconds),
		"{NODEID}", markNodeID, "{CONSENSUS}", markConsensus, "{BINDING}", markBinding,
		"{CHAINID}", clusterops.ShellQuote(in.ChainID), "{OPERATOR}", clusterops.ShellQuote(in.Operator),
		"{SERVICE}", clusterreg.HotKeyService,
	).Replace(`set -eu
HK={PROVIDER}/hot-key
if [ ! -f "$HK" ]; then
  {CLI} global stop provider || true
  {CLI} global start provider
  waited=0
  while [ ! -f "$HK" ]; do
    [ "$waited" -lt {WAIT} ] || { echo "the provider did not create $HK" >&2; exit 1; }
    sleep 1; waited=$((waited + 1))
  done
  {CLI} global stop provider
fi
echo {NODEID}
runuser -u {CHAINUSER} -- {ORAMAD} --home {CHAINHOME} comet show-node-id
echo {CONSENSUS}
runuser -u {CHAINUSER} -- {ORAMAD} --home {CHAINHOME} comet show-validator
echo {BINDING}
{CLI} global bind --chain-id {CHAINID} --operator {OPERATOR} --service {SERVICE} --key-file "$HK" --key-type secp256k1
`)
	if !in.BindConsensus {
		return script
	}
	return script + strings.NewReplacer(
		"{CONSBINDING}", markConsensusBinding, "{CLI}", constants.GlobalBinDir+"/orama", "{VALKEY}", constants.ChainValidatorKeyPath,
		"{CHAINID}", clusterops.ShellQuote(in.ChainID), "{OPERATOR}", clusterops.ShellQuote(in.Operator), "{SERVICE}", clusterreg.ConsensusService,
	).Replace(`echo {CONSBINDING}
{CLI} global bind --chain-id {CHAINID} --operator {OPERATOR} --service {SERVICE} --key-file {VALKEY}
`)
}

// ParseIdentity reads identityScript's output into the facts a registration
// needs. Every value is checked: the node id, the consensus key and the binding
// are about to be signed over.
func ParseIdentity(out string) (NodeIdentity, error) {
	sec := splitMarked(out, markNodeID, markConsensus, markBinding, markConsensusBinding)
	var id NodeIdentity
	id.ChainNodeID = strings.TrimSpace(sec[markNodeID])
	if b, err := hex.DecodeString(id.ChainNodeID); err != nil || len(b) != 20 {
		return id, fmt.Errorf("the chain node id %q is not 20 bytes of hex", id.ChainNodeID)
	}
	var key struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(sec[markConsensus]), &key); err != nil {
		return id, fmt.Errorf("the consensus key is not JSON: %w", err)
	}
	consensus, err := base64.StdEncoding.DecodeString(key.Key)
	if err != nil || len(consensus) != clusterreg.ConsensusPubKeyLen {
		return id, fmt.Errorf("the consensus key %q is not a base64 ed25519 key", key.Key)
	}
	id.ConsensusPubKey = consensus
	binding, hotKey, err := parseBinding(sec[markBinding])
	if err != nil {
		return id, err
	}
	id.HotBinding, id.HotKey = binding, hotKey
	if raw, ok := sec[markConsensusBinding]; ok {
		if id.ConsensusBinding, err = parseConsensusBinding(raw, id.ConsensusPubKey); err != nil {
			return id, err
		}
	}
	return id, nil
}

// parseConsensusBinding reads the consensus key's binding and checks it is for
// the key the chain node reported: a binding of another key would attribute the
// wrong validator to the operator.
func parseConsensusBinding(raw string, consensus []byte) (*clusterreg.NodeBinding, error) {
	b, err := parseBindingDoc(raw, clusterreg.ConsensusService)
	if err != nil {
		return nil, fmt.Errorf("the consensus key's binding: %w", err)
	}
	if b.KeyType != globalbind.KeyTypeEd25519 || !bytes.Equal(b.Pubkey, consensus) {
		return nil, fmt.Errorf("the consensus key's binding is for %s key %x, not the chain node's ed25519 key %x", b.KeyType, b.Pubkey, consensus)
	}
	return &b, nil
}

func parseBinding(raw string) (clusterreg.NodeBinding, string, error) {
	b, err := parseBindingDoc(raw, clusterreg.HotKeyService)
	if err != nil {
		return clusterreg.NodeBinding{}, "", fmt.Errorf("the hot key's binding: %w", err)
	}
	address, err := clusterreg.AccountAddressOf(b.Pubkey)
	if err != nil {
		return clusterreg.NodeBinding{}, "", fmt.Errorf("the hot key's address: %w", err)
	}
	return b, address, nil
}

// parseBindingDoc reads the JSON `orama global bind` prints, for the service
// wanted.
func parseBindingDoc(raw, service string) (clusterreg.NodeBinding, error) {
	var doc struct {
		Service   string `json:"service"`
		KeyType   string `json:"key_type"`
		Pubkey    string `json:"pubkey"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return clusterreg.NodeBinding{}, fmt.Errorf("the binding is not JSON: %w", err)
	}
	pub, err := hex.DecodeString(doc.Pubkey)
	if err != nil {
		return clusterreg.NodeBinding{}, fmt.Errorf("the binding's pubkey is not hex: %w", err)
	}
	sig, err := hex.DecodeString(doc.Signature)
	if err != nil {
		return clusterreg.NodeBinding{}, fmt.Errorf("the binding's signature is not hex: %w", err)
	}
	if doc.Service != service {
		return clusterreg.NodeBinding{}, fmt.Errorf("the binding is for service %q, not %q", doc.Service, service)
	}
	return clusterreg.NodeBinding{Service: doc.Service, KeyType: doc.KeyType, Pubkey: pub, Signature: sig}, nil
}

// startServicesScript writes the node id the provider reads and starts it.
func startServicesScript() string {
	return fmt.Sprintf(`set -eu
id=$(cat)
dir=%s
printf '%%s\n' "$id" | runuser -u `+providerAccount+` -- sh -c 'umask 022; tmp=$(mktemp "$1/.nid.XXXXXX") && cat >"$tmp" && mv -f "$tmp" "$1/node-id"' _ "$dir"
%s/orama global start provider
`, constants.GlobalProviderHome, constants.GlobalBinDir)
}
