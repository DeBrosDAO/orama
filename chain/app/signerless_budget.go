package app

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	shieldedante "github.com/DeBrosOfficial/network/chain/x/shielded/ante"
)

// The app uses no mempool (NoOpMempool), so the SDK's ProcessProposal accepts any block and the
// per-block limit on signer-less shielded transfers, which bounds the proof work a block asks of
// every validator, is otherwise enforced only while the block executes, after all of its
// transfers have been admitted. The proposal handlers enforce it up front: PrepareProposal
// leaves the surplus out of the block it builds and ProcessProposal refuses a block over the
// limit. Both count every transaction of the block, the transactions an inclusion list forces
// included. A signer-less transfer carries no signature, so it has no sender or sequence for a
// list to name and no list can force one (TestInclusion_aSignerlessTransferCannotBeListed): the
// forced prefix never holds any, and an honest proposer's block always passes its own check.

// signerlessLimit reads x/shielded's max_signerless_per_block from the state the proposal is
// judged against, which every validator shares.
func (h *inclusionHandlers) signerlessLimit(ctx sdk.Context) (int, error) {
	max, err := h.signerlessMax(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to read the signer-less limit: %w", err)
	}
	return int(max), nil
}

// isSignerless reports whether raw decodes to a signer-less shielded transfer. A transaction that
// does not decode is not one: the SDK handlers decide what to do with it.
func (h *inclusionHandlers) isSignerless(raw []byte) bool {
	tx, err := h.rules.decode(raw)
	return err == nil && shieldedante.IsSignerless(tx)
}

func (h *inclusionHandlers) countSignerless(txs [][]byte) int {
	n := 0
	for _, raw := range txs {
		if h.isSignerless(raw) {
			n++
		}
	}
	return n
}

// capSignerless returns txs, in order, without the signer-less transfers past the first room.
func (h *inclusionHandlers) capSignerless(txs [][]byte, room int) [][]byte {
	out := make([][]byte, 0, len(txs))
	for _, raw := range txs {
		if h.isSignerless(raw) {
			if room <= 0 {
				continue
			}
			room--
		}
		out = append(out, raw)
	}
	return out
}
