package app

import (
	"bytes"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"

	"github.com/DeBrosOfficial/network/chain/x/inclusion"
)

// injectedCommitMagic starts the transaction a proposer puts first in a block
// to carry the previous height's ExtendedCommitInfo. Its first byte, 'O', is
// protobuf field 9 with wire type 7, which is not a legal wire type, so these
// bytes can never decode as a chain transaction.
const injectedCommitMagic = "ORAMA-INCLUSION-EXTENDED-COMMIT-V1:"

// blockOverheadReserve is subtracted from the consensus block max bytes to
// get the budget for listed transactions: the header, last commit, evidence
// and per-transaction framing all live outside the transaction bytes.
const blockOverheadReserve = 2 * 1024 * 1024

func isInjectedCommit(tx []byte) bool {
	return bytes.HasPrefix(tx, []byte(injectedCommitMagic))
}

func encodeInjectedCommit(ec abci.ExtendedCommitInfo) ([]byte, error) {
	body, err := ec.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal the extended commit: %w", err)
	}
	return append([]byte(injectedCommitMagic), body...), nil
}

// decodeInjectedCommit parses the injected transaction. It rejects a payload
// that is not the canonical encoding of what it decodes to, so a proposer
// cannot pad the block with unknown fields.
func decodeInjectedCommit(tx []byte) (abci.ExtendedCommitInfo, error) {
	var ec abci.ExtendedCommitInfo
	if !isInjectedCommit(tx) {
		return ec, fmt.Errorf("missing the injected extended commit")
	}
	body := tx[len(injectedCommitMagic):]
	if err := ec.Unmarshal(body); err != nil {
		return ec, fmt.Errorf("failed to unmarshal the extended commit: %w", err)
	}
	again, err := ec.Marshal()
	if err != nil || !bytes.Equal(again, body) {
		return ec, fmt.Errorf("the extended commit is not canonically encoded")
	}
	return ec, nil
}

// commitOf turns an extended commit into the library's Commit. Only votes for
// the block count. A vote whose extension does not decode or does not pass
// ValidateList is dropped, as PrepareCommit drops an invalid extension: a
// late precommit can reach a commit without VerifyVoteExtension having run on
// it. The second return is the whole set's power, dropped votes included.
func commitOf(view inclusion.View, height int64, ec abci.ExtendedCommitInfo) (inclusion.Commit, int64) {
	var total int64
	exts := make([]inclusion.Extension, 0, len(ec.Votes))
	for _, vote := range ec.Votes {
		total += vote.Validator.Power
		if vote.BlockIdFlag != cmtproto.BlockIDFlagCommit {
			continue
		}
		txs, err := inclusion.DecodeList(vote.VoteExtension)
		if err != nil || inclusion.ValidateList(view, txs) != nil {
			continue
		}
		exts = append(exts, inclusion.Extension{
			PubKey: vote.Validator.Address,
			Power:  vote.Validator.Power,
			Height: height - 1,
			Round:  ec.Round,
			Txs:    txs,
		})
	}
	return inclusion.Commit{Extensions: exts}, total
}

func protoSize(txs ...[]byte) int64 {
	wrapped := make([]cmttypes.Tx, len(txs))
	for i, tx := range txs {
		wrapped[i] = tx
	}
	return cmttypes.ComputeProtoSizeForTxs(wrapped)
}
