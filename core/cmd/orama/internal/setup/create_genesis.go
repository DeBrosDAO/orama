package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// The genesis of a network setup creates is built the way chain/scripts/stagenet/
// deploy.sh built stagenet's: oramad's own `genesis` commands in a scratch home,
// on the first machine, from every machine's public keys; then one edit of the
// consensus parameters, which no module owns.
const (
	// genesisMoniker names the scratch home's `oramad init`; it is not a node.
	genesisMoniker = "genesis-work"
	// testEpochDuration and testMinBlocksPerEpoch are the epoch of a test network
	// (deploy.sh EPOCH_DURATION and EPOCH_MIN_BLOCKS): short enough that a
	// network has earnings, and a faucet that can pay, within minutes. A
	// production chain id keeps the chain's own defaults (24h, 14,400 blocks).
	testEpochDuration     = "300s"
	testMinBlocksPerEpoch = 10
	// testFaucetMaxDripNorama is the largest single faucet drip of a test network,
	// 10,000 ORAMA: the chain's default of 1,000
	// ORAMA is less than one newcomer's setup needs, since the validator's
	// self-bond alone is 1,000 ORAMA. The epoch cap keeps its default.
	testFaucetMaxDripNorama = "10000000000000"
	// consensusMaxGas is the finite block gas limit set in genesis (deploy.sh):
	// x/consensus has no genesis state of its own, so the top-level consensus
	// field would otherwise keep CometBFT's unlimited default.
	consensusMaxGas = "100000000"
	// consensusMaxBlockBytes is the block size set in genesis, CometBFT's default of 21 MiB written
	// out so the genesis does not depend on a default. chain/app/consensus_params.go refuses any
	// other value on a production chain id (TestConsensusConstants_equalTheChainsPins).
	consensusMaxBlockBytes = "22020096"
	// voteExtensionsEnableHeight turns vote extensions on at height 2 (deploy.sh
	// VOTE_EXTENSIONS_ENABLE_HEIGHT; docs/whitepaper/technical-reference/vol2/39-chain-architecture.md, C13 inclusion lists): they
	// are a genesis-only switch, height 2 leaves block 1 an ordinary block and
	// exercises the enable transition.
	voteExtensionsEnableHeight = "2"
	// validatorKeyType is the only validator key type a genesis lists: x/power decodes every bonded
	// validator's key as raw ed25519 and halts the chain on any other (chain/app/consensus_params.go).
	validatorKeyType = "ed25519"
	// evidenceMaxAgeNumBlocks and evidenceMaxAgeDurationNanos are the evidence window of the genesis:
	// 14 days, the block retention (x/archive DefaultBlocksIn14Days blocks at the 5-second block
	// interval; pkg/install chainMinRetainBlocks), since CometBFT can only verify evidence against
	// blocks a node still holds. evidenceMaxBytes is CometBFT's default written out. chain/app/
	// consensus_params.go refuses any other value on a production chain id
	// (TestConsensusConstants_equalTheChainsPins).
	evidenceMaxAgeNumBlocks     = "241920"
	evidenceMaxAgeDurationNanos = "1209600000000000"
	evidenceMaxBytes            = "1048576"
	// genesisWorkName is the scratch home's directory beside the chain home.
	genesisWorkName = "genesis-work"
)

// genesisWorkDir is the scratch home the genesis is built in, beside the chain
// home (deploy.sh GENESIS_WORK).
var genesisWorkDir = constants.GlobalStateRoot + "/" + genesisWorkName

// Seat is one bootstrap committee member: a machine's seat in x/power's genesis.
type Seat struct {
	// Moniker is the node's name.
	Moniker string
	// Address is the seat's account, the key named "validator" in oramad's test
	// keyring on the machine: it receives the seat's share and signs the faucet.
	Address string
	// ConsensusPubKey is the base64 of the machine's raw ed25519 consensus key.
	ConsensusPubKey string
	// NodeID is the CometBFT node id, for the persistent peers.
	NodeID string
}

// GenesisSpec is what the genesis is built from.
type GenesisSpec struct {
	ChainID string
	Seats   []Seat
	// TestNetwork says the chain id is not a production one: short epochs and a
	// genesis that allows a supply, which a production chain refuses.
	TestNetwork bool
	Faucet      bool
}

// GenesisSteps are the arguments of every oramad invocation that builds the
// genesis, in order, after `--home <scratch home>`. The committee size, which x/power
// checks against the number of members, is the number of seats and is given with the
// first one (the flag overwrites a parameter).
func GenesisSteps(s GenesisSpec) [][]string {
	steps := [][]string{{"init", genesisMoniker, "--chain-id", s.ChainID, "--default-denom", constants.ChainDenom}}
	if s.TestNetwork {
		emission := []string{"genesis", "set-emission-params", "--epoch-duration", testEpochDuration,
			"--min-blocks-per-epoch", strconv.Itoa(testMinBlocksPerEpoch), "--allow-bootstrap-stake"}
		if s.Faucet {
			emission = append(emission, "--faucet-enabled", "--faucet-max-drip", testFaucetMaxDripNorama)
		}
		steps = append(steps, emission)
	}
	for i, seat := range s.Seats {
		add := []string{"genesis", "add-bootstrap-validator", seat.Address, "--moniker", seat.Moniker, "--consensus-pubkey-base64", seat.ConsensusPubKey}
		if i == 0 {
			add = append(add, "--min-committee-size", strconv.Itoa(len(s.Seats)))
		}
		steps = append(steps, add)
	}
	return append(steps, []string{"genesis", "add-standard-contracts"}, []string{"genesis", "validate"})
}

// genesisScript builds the genesis on the machine and prints it. The scratch home
// is the chain account's alone and is removed whatever happens.
func genesisScript(steps [][]string) string {
	oramad := constants.GlobalBinDir + "/" + constants.ChainDaemonName
	var b strings.Builder
	fmt.Fprintf(&b, "set -eu\nwork=%s\ntrap 'rm -rf \"$work\"' EXIT\nrm -rf \"$work\"\ninstall -d -m 0700 -o %[2]s -g %[2]s \"$work\"\n", shellArg(genesisWorkDir), constants.ChainUser)
	for _, step := range steps {
		quoted := make([]string, len(step))
		for i, a := range step {
			quoted[i] = shellArg(a)
		}
		fmt.Fprintf(&b, "runuser -u %s -- %s --home \"$work\" %s >/dev/null\n", constants.ChainUser, oramad, strings.Join(quoted, " "))
	}
	fmt.Fprintf(&b, "runuser -u %s -- cat \"$work/config/genesis.json\"\n", constants.ChainUser)
	return b.String()
}

// ApplyConsensusParams sets the consensus parameters no module owns in a
// genesis built by oramad: a finite block max_gas, the block max_bytes, the height
// vote extensions turn on at, ed25519 as the only validator key type and the evidence
// parameters. oramad refuses a genesis whose values differ on a
// production chain id (chain/app/consensus_params.go). Everything else in the document is kept as it is; the result is
// indented and its keys sorted, so applying it again changes nothing.
func ApplyConsensusParams(genesis []byte) ([]byte, error) {
	doc, err := objectOf(genesis, "genesis")
	if err != nil {
		return nil, err
	}
	err = editObject(doc, "consensus", func(consensus map[string]json.RawMessage) error {
		return editObject(consensus, "params", func(params map[string]json.RawMessage) error {
			if err := editObject(params, "block", func(block map[string]json.RawMessage) error {
				if err := setString(block, "max_bytes", consensusMaxBlockBytes); err != nil {
					return err
				}
				return setString(block, "max_gas", consensusMaxGas)
			}); err != nil {
				return err
			}
			if err := editObject(params, "evidence", func(evidence map[string]json.RawMessage) error {
				if err := setString(evidence, "max_age_num_blocks", evidenceMaxAgeNumBlocks); err != nil {
					return err
				}
				if err := setString(evidence, "max_age_duration", evidenceMaxAgeDurationNanos); err != nil {
					return err
				}
				return setString(evidence, "max_bytes", evidenceMaxBytes)
			}); err != nil {
				return err
			}
			if err := editObject(params, "validator", func(validator map[string]json.RawMessage) error {
				raw, err := json.Marshal([]string{validatorKeyType})
				if err != nil {
					return fmt.Errorf("write pub_key_types: %w", err)
				}
				validator["pub_key_types"] = raw
				return nil
			}); err != nil {
				return err
			}
			return editObject(params, "abci", func(abci map[string]json.RawMessage) error {
				return setString(abci, "vote_extensions_enable_height", voteExtensionsEnableHeight)
			})
		})
	})
	if err != nil {
		return nil, fmt.Errorf("set the consensus parameters of the genesis: %w", err)
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("write the genesis: %w", err)
	}
	return out.Bytes(), nil
}

func objectOf(raw []byte, what string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", what, err)
	}
	return obj, nil
}

// editObject applies edit to the object at key, creating it when it is missing.
func editObject(parent map[string]json.RawMessage, key string, edit func(map[string]json.RawMessage) error) error {
	obj := map[string]json.RawMessage{}
	if raw, ok := parent[key]; ok && string(bytes.TrimSpace(raw)) != "null" {
		var err error
		if obj, err = objectOf(raw, key); err != nil {
			return err
		}
	}
	if err := edit(obj); err != nil {
		return err
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	parent[key] = raw
	return nil
}

// setString sets key to the JSON string value: CometBFT's genesis JSON writes
// 64-bit integers as strings.
func setString(obj map[string]json.RawMessage, key, value string) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	obj[key] = raw
	return nil
}
