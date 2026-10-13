package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	archivetypes "github.com/DeBrosOfficial/network/chain/x/archive/types"
)

// The consensus parameters live in the genesis document's top-level consensus field, not in
// app_state, so ValidateLockedGenesis never sees them. Three of them bound the work and the
// fees of every block: block.max_gas (-1 is unlimited, and the base fee and the shielded proof work
// are bounded by it), block.max_bytes, and abci.vote_extensions_enable_height (0 switches the
// inclusion lists off). Two more decide who can halt the chain and what can be punished:
// validator.pub_key_types, because x/power reads every bonded validator's key as raw ed25519 and
// returns an error from EndBlock (a halt) for any other type, while staking accepts whatever types
// the consensus parameters list; and the evidence parameters, which bound how old (14 days, the
// block retention) and how large the evidence of a double sign may be. They are validated here from InitChain's request, by chain-id
// class like the locked parameters (params.ClassOfChainID):
//   - orama-localnet-<name>: free.
//   - orama-devnet-<name> and orama-stagenet-<name>: a finite block gas limit, a block size above
//     zero and not over ConsensusMaxBlockBytes, vote extensions on from some height, and ed25519 as
//     the only validator key type.
//   - anything else (production): exactly the values `orama setup --create-network` writes.
const (
	// ConsensusMaxGas is the block gas limit of a genesis (core/cmd/orama/internal/setup
	// consensusMaxGas, chain/scripts/stagenet/deploy.sh).
	ConsensusMaxGas int64 = 100_000_000
	// ConsensusMaxBlockBytes is the block size of a genesis, CometBFT's default of 21 MiB, and the
	// most a test network may set. It equals x/inclusion's DefaultMaxBlockBytes, the budget the
	// inclusion handlers fall back on.
	ConsensusMaxBlockBytes int64 = 21 * 1024 * 1024
	// ConsensusVoteExtensionsEnableHeight is the height vote extensions turn on at (core/cmd/orama/
	// internal/setup voteExtensionsEnableHeight): block 1 stays an ordinary block.
	ConsensusVoteExtensionsEnableHeight int64 = 2
	// ConsensusValidatorKeyType is the only validator key type a genesis may list: x/power decodes
	// every bonded validator's key as raw ed25519.
	ConsensusValidatorKeyType = "ed25519"
	// ConsensusEvidenceMaxAgeNumBlocks and ConsensusEvidenceMaxAgeDurationNanos are the evidence
	// window of a genesis: 14 days, the same window every node retains blocks for (x/archive's
	// DefaultBlocksIn14Days; core/pkg/install min-retain-blocks follows it). CometBFT can only
	// verify evidence against the blocks and validator sets a node still holds, so the window may
	// not be longer than the retention, and it stays under the 21-day unbonding time so a
	// validator cannot unbond before its evidence expires (TestConsensusEvidenceWindow_*). The
	// duration is the block count at the block interval, so the two never disagree.
	// core/cmd/orama/internal/setup evidenceMaxAgeNumBlocks and evidenceMaxAgeDurationNanos write
	// the same values.
	ConsensusEvidenceMaxAgeNumBlocks     int64 = archivetypes.DefaultBlocksIn14Days
	ConsensusEvidenceMaxAgeDurationNanos int64 = ConsensusEvidenceMaxAgeNumBlocks * params.BlockIntervalSeconds * int64(time.Second)
	// ConsensusEvidenceMaxBytes is CometBFT's default evidence size (core/cmd/orama/internal/setup
	// evidenceMaxBytes), written out so a production genesis does not depend on a default.
	ConsensusEvidenceMaxBytes int64 = 1_048_576
)

// ValidateConsensusParams returns an error naming every consensus parameter of cp that the class of
// chainID does not allow. A nil cp is refused on every class but a localnet.
func ValidateConsensusParams(chainID string, cp *cmtproto.ConsensusParams) error {
	class := params.ClassOfChainID(chainID)
	if class == params.ChainLocalnet {
		return nil
	}
	if cp == nil {
		return fmt.Errorf("InitChain carries no consensus parameters on chain-id %q", chainID)
	}
	production := class == params.ChainProduction
	var maxGas, maxBytes, voteHeight int64
	if cp.Block != nil {
		maxGas, maxBytes = cp.Block.MaxGas, cp.Block.MaxBytes
	}
	if cp.Abci != nil {
		voteHeight = cp.Abci.VoteExtensionsEnableHeight
	}
	var diffs []string
	switch {
	case production && maxGas != ConsensusMaxGas:
		diffs = append(diffs, fmt.Sprintf("block.max_gas is %d, want %d", maxGas, ConsensusMaxGas))
	case maxGas <= 0:
		diffs = append(diffs, fmt.Sprintf("block.max_gas is %d, want a finite limit above zero", maxGas))
	}
	switch {
	case production && maxBytes != ConsensusMaxBlockBytes:
		diffs = append(diffs, fmt.Sprintf("block.max_bytes is %d, want %d", maxBytes, ConsensusMaxBlockBytes))
	case maxBytes <= 0 || maxBytes > ConsensusMaxBlockBytes:
		diffs = append(diffs, fmt.Sprintf("block.max_bytes is %d, want above zero and at most %d", maxBytes, ConsensusMaxBlockBytes))
	}
	switch {
	case production && voteHeight != ConsensusVoteExtensionsEnableHeight:
		diffs = append(diffs, fmt.Sprintf("abci.vote_extensions_enable_height is %d, want %d", voteHeight, ConsensusVoteExtensionsEnableHeight))
	case voteHeight <= 0:
		diffs = append(diffs, fmt.Sprintf("abci.vote_extensions_enable_height is %d, want the height they turn on at (above zero)", voteHeight))
	}
	if cp.Validator == nil || len(cp.Validator.PubKeyTypes) != 1 || cp.Validator.PubKeyTypes[0] != ConsensusValidatorKeyType {
		var types []string
		if cp.Validator != nil {
			types = cp.Validator.PubKeyTypes
		}
		diffs = append(diffs, fmt.Sprintf("validator.pub_key_types is %v, want [%s]: x/power halts the chain on any other key type", types, ConsensusValidatorKeyType))
	}
	if production {
		diffs = append(diffs, evidenceDiffs(cp.Evidence)...)
	}
	if len(diffs) == 0 {
		return nil
	}
	sort.Strings(diffs)
	return fmt.Errorf("consensus parameters differ from the allowed values on chain-id %q:\n  %s", chainID, strings.Join(diffs, "\n  "))
}

// evidenceDiffs lists every evidence parameter that is not the pinned value (nil evidence is all zero).
func evidenceDiffs(e *cmtproto.EvidenceParams) []string {
	var numBlocks, duration, maxBytes int64
	if e != nil {
		numBlocks, duration, maxBytes = e.MaxAgeNumBlocks, int64(e.MaxAgeDuration), e.MaxBytes
	}
	var diffs []string
	if numBlocks != ConsensusEvidenceMaxAgeNumBlocks {
		diffs = append(diffs, fmt.Sprintf("evidence.max_age_num_blocks is %d, want %d", numBlocks, ConsensusEvidenceMaxAgeNumBlocks))
	}
	if duration != ConsensusEvidenceMaxAgeDurationNanos {
		diffs = append(diffs, fmt.Sprintf("evidence.max_age_duration is %d ns, want %d ns", duration, ConsensusEvidenceMaxAgeDurationNanos))
	}
	if maxBytes != ConsensusEvidenceMaxBytes {
		diffs = append(diffs, fmt.Sprintf("evidence.max_bytes is %d, want %d", maxBytes, ConsensusEvidenceMaxBytes))
	}
	return diffs
}
