package setup

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/clusterops"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// The commands setup runs on a seat. Like scripts.go, they are built here with
// every value quoted, so the tests read exactly what reaches the machine.

const (
	markSeat      = "__SEAT__"
	markGenesis   = "__GENESIS__"
	markStarted   = "__STARTED__"
	markCommittee = "__COMMITTEE__"
	// seatKeyName is the key of the seat's account in oramad's test keyring: the
	// name `orama chain faucet` and chain/scripts/stagenet/deploy.sh sign with.
	seatKeyName = "validator"
)

var chainRESTEpoch = fmt.Sprintf("http://%s:%d/orama/emission/v1/current-epoch", constants.GlobalNetnsAddr, constants.ChainAPIPort)

// chainOramad is the chain binary as the chain account runs it, with its home.
func chainOramad() string {
	return fmt.Sprintf("runuser -u %s -- %s/%s --home %s", constants.ChainUser, constants.GlobalBinDir, constants.ChainDaemonName, constants.ChainHome)
}

// placeholderGenesis is the genesis `--init-chain` is given before the real one
// exists: the installer only needs it to name the chain.
func placeholderGenesis(chainID string) []byte {
	return []byte(fmt.Sprintf("{\"chain_id\":%q}\n", chainID))
}

// InitChainCommand is the first `orama global install` of a seat: the services,
// the chain home made with a placeholder genesis, the external address. The
// peers are not known yet and nothing is started.
func InitChainCommand(in InitChainInput) string {
	q := clusterops.ShellQuote
	parts := installBase(in.Node)
	parts = append(parts, "--init-chain", "--chain-id", q(in.ChainID), "--moniker", q(in.Node.Name), "--genesis", q(globalStageDir+"/genesis.json"))
	return strings.Join(append(parts, installTail(in.Node, in.IP, in.User, in.Contact)...), " ")
}

// WireChainCommand is the same install again, with the persistent peers: the
// chain unit is rewritten to dial them. Without --init-chain the chain home,
// its keys and its genesis are kept.
func WireChainCommand(in WireInput) string {
	parts := installBase(in.Node)
	if in.Peers != "" {
		parts = append(parts, "--persistent-peers", clusterops.ShellQuote(in.Peers))
	}
	return strings.Join(append(parts, installTail(in.Node, in.IP, in.User, in.Contact)...), " ")
}

// installBase is what both installs of a seat share with a joiner's
// (GlobalInstallCommand): the services, the staged release and the public storage.
func installBase(n NodePlan) []string {
	return []string{"sudo", orama, "global", "install", "--colocated",
		"--services", clusterops.ShellQuote(strings.Join(n.ServiceNames(), ",")),
		"--staged-dir", stagedBinDir, "--manifest", install.DefaultStagedManifest,
		"--public-storage-gb", strconv.FormatUint(n.StorageGB, 10)}
}

// installTail is the external address, the chain client and the relay's options.
func installTail(n NodePlan, ip, user, contact string) []string {
	q := clusterops.ShellQuote
	parts := []string{"--external-address", q(fmt.Sprintf("%s:%d", ip, constants.ChainP2PPort))}
	if user != DefaultSSHUser {
		parts = append(parts, "--chain-client-user", q(user))
	}
	if n.HasService(install.GlobalServiceRelay) {
		parts = append(parts, "--tor-address", q(ip), "--tor-contact", q(contact), "--tor-node-id", q(n.Name))
	}
	return parts
}

// seatScript prints the node's id, its consensus key and its seat account,
// making the account first when the machine has none. Only public values are
// printed; the keyring is oramad's test backend, unencrypted, on this machine.
func seatScript() string {
	cmd := chainOramad()
	return strings.NewReplacer("{CMD}", cmd, "{KEY}", seatKeyName, "{NODEID}", markNodeID, "{CONSENSUS}", markConsensus, "{SEAT}", markSeat).Replace(`set -eu
if ! {CMD} keys show {KEY} --keyring-backend test >/dev/null 2>&1; then
  {CMD} keys add {KEY} --keyring-backend test --no-backup >/dev/null
fi
echo {NODEID}
{CMD} comet show-node-id
echo {CONSENSUS}
{CMD} comet show-validator
echo {SEAT}
{CMD} keys show {KEY} -a --keyring-backend test
`)
}

// ParseSeat reads seatScript's output. Every value is checked: they are about to
// go into a genesis and into other machines' command lines.
func ParseSeat(out string) (Seat, error) {
	sec := splitMarked(out, markNodeID, markConsensus, markSeat)
	var s Seat
	s.NodeID = strings.TrimSpace(sec[markNodeID])
	if b, err := hex.DecodeString(s.NodeID); err != nil || len(b) != 20 {
		return s, fmt.Errorf("the chain node id %q is not 20 bytes of hex", s.NodeID)
	}
	var key struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(sec[markConsensus]), &key); err != nil {
		return s, fmt.Errorf("the consensus key is not JSON: %w", err)
	}
	if raw, err := base64.StdEncoding.DecodeString(key.Key); err != nil || len(raw) != clusterreg.ConsensusPubKeyLen {
		return s, fmt.Errorf("the consensus key %q is not a base64 ed25519 key", key.Key)
	}
	s.ConsensusPubKey = key.Key
	s.Address = strings.TrimSpace(sec[markSeat])
	if _, err := clusterreg.CanonicalAccount(s.Address); err != nil {
		return s, fmt.Errorf("the seat account %q is not an orama address: %w", s.Address, err)
	}
	return s, nil
}

// homeStateScript prints what the chain home holds: whether the genesis is there
// and how many bootstrap validators it names, its digest, and whether a chain has
// run here. It reads as the chain account, which owns the home; a machine that has
// no such account yet has no home either.
func homeStateScript() string {
	run := "runuser -u " + constants.ChainUser + " -- "
	return strings.NewReplacer("{RUN}", run, "{GEN}", constants.ChainGenesisPath, "{DATA}", constants.ChainHome+"/data", "{USER}", constants.ChainUser,
		"{GENESIS}", markGenesis, "{COMMITTEE}", markCommittee, "{STARTED}", markStarted).Replace(`set -eu
if ! id -u {USER} >/dev/null 2>&1; then
  printf '%s\nnone\n%s\n0\n%s\n0\n' {GENESIS} {COMMITTEE} {STARTED}
  exit 0
fi
echo {GENESIS}
if {RUN}test -f {GEN}; then
  {RUN}sha256sum {GEN} | cut -d' ' -f1
else
  echo none
fi
echo {COMMITTEE}
{RUN}sh -c 'test -f "$1" && grep -o "\"consensus_pubkey\"" "$1" | wc -l || echo 0' _ {GEN}
echo {STARTED}
{RUN}sh -c 'ls -d "$1"/blockstore.db* >/dev/null 2>&1 && echo 1 || echo 0' _ {DATA}
`)
}

// ParseHomeState reads homeStateScript's output.
func ParseHomeState(out string) (HomeState, error) {
	sec := splitMarked(out, markGenesis, markCommittee, markStarted)
	var h HomeState
	switch sum := strings.TrimSpace(sec[markGenesis]); {
	case sum == "none":
	case sha256Hex.MatchString(sum):
		h.Genesis, h.SHA256 = true, sum
	default:
		return h, fmt.Errorf("the genesis digest %q is not a SHA-256", sum)
	}
	members, err := strconv.Atoi(strings.TrimSpace(sec[markCommittee]))
	if err != nil {
		return h, fmt.Errorf("the committee count %q is not a number", strings.TrimSpace(sec[markCommittee]))
	}
	h.Final = h.Genesis && members > 0
	switch started := strings.TrimSpace(sec[markStarted]); started {
	case "0", "1":
		h.Started = started == "1"
	default:
		return h, fmt.Errorf("the block store check printed %q, not 0 or 1", started)
	}
	return h, nil
}

// putGenesisCommand writes stdin over the chain home's genesis as the chain
// account: a root process would follow a link the chain account planted there.
func putGenesisCommand() string {
	return bash("sudo ", fmt.Sprintf(`set -eu
dir=%s
runuser -u %s -- sh -c 'umask 077; tmp=$(mktemp "$1/.genesis.XXXXXX") && cat >"$tmp" && mv -f "$tmp" "$1/genesis.json"' _ "$dir"`,
		constants.ChainHome+"/config", constants.ChainUser))
}

// readGenesisCommand prints the genesis in the chain home.
func readGenesisCommand() string {
	return bash("sudo ", "runuser -u "+constants.ChainUser+" -- cat "+constants.ChainGenesisPath)
}

// epochCommand prints the chain's current epoch state.
func epochCommand() string {
	return "curl -fsS --max-time 10 " + chainRESTEpoch
}

// ParseEpoch reads x/emission's current-epoch answer.
func ParseEpoch(body string) (uint64, error) {
	var doc struct {
		EpochState struct {
			CurrentEpoch string `json:"current_epoch"`
		} `json:"epoch_state"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return 0, fmt.Errorf("the chain's epoch answer is not JSON: %w (%.80q)", err, body)
	}
	epoch, err := strconv.ParseUint(doc.EpochState.CurrentEpoch, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("the chain's current_epoch %q is not a number", doc.EpochState.CurrentEpoch)
	}
	return epoch, nil
}

// ParseChainHealth reads chainStateScript's output as a poll of a starting chain:
// an RPC that has not answered is not an error, it is RPCUp false.
func ParseChainHealth(out string) (ChainHealth, error) {
	st, err := ParseChainState(out)
	if err != nil {
		return ChainHealth{}, err
	}
	answered := strings.TrimSpace(splitMarked(out, markStatus, markActive, markLog)[markStatus]) != ""
	return ChainHealth{Running: st.Running, RPCUp: answered, Height: st.Height, CatchingUp: st.CatchingUp, Detail: st.Detail}, nil
}
