package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/storage/types"
)

func genesisWithSettlement(s types.Settlement) *types.GenesisState {
	gs := types.DefaultGenesisState()
	gs.QueueHead = 0
	gs.QueueTail = 1
	s.EscrowPay, s.MintPay, s.ArchiveTopUp = math.ZeroInt(), math.ZeroInt(), math.ZeroInt()
	gs.Settlements = []types.Settlement{s}
	return gs
}

func TestGenesisValidate_penaltyRowMustPayNothing(t *testing.T) {
	require.NoError(t, genesisWithSettlement(types.Settlement{Seq: 0, PenaltyOnly: true, NodeId: "n"}).Validate())

	gs := genesisWithSettlement(types.Settlement{Seq: 0, PenaltyOnly: true, NodeId: "n"})
	gs.Settlements[0].MintPay = math.NewInt(1)
	require.ErrorContains(t, gs.Validate(), "must be a miss that pays nothing")

	require.ErrorContains(t, genesisWithSettlement(types.Settlement{Seq: 0, PenaltyOnly: true, Proved: true}).Validate(), "must be a miss that pays nothing")
}

func TestGenesisValidate_retryFieldsAreAccepted(t *testing.T) {
	gs := genesisWithSettlement(types.Settlement{Seq: 0, Proved: true, Attempts: 2, NotBeforeEpoch: 9})
	require.NoError(t, gs.Validate())
}
