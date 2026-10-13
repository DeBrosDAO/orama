package types_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

func TestProposalRoundTrip(t *testing.T) {
	original := types.Proposal{
		Id:                7,
		Proposer:          "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0dead",
		SubmitUnixNano:    10,
		VotingEndUnixNano: 20,
		Status:            types.ProposalStatus_VOTING,
		Content: types.ProposalContent{DevelopmentSpend: &types.DevelopmentSpend{
			Recipient: "orama1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0dead",
			Amount:    math.NewInt(42),
			Epoch:     3,
		}},
		TokenYes:     math.ZeroInt(),
		TokenNo:      math.NewInt(5),
		TokenAbstain: math.ZeroInt(),
	}
	bz, err := original.Marshal()
	require.NoError(t, err)
	var decoded types.Proposal
	require.NoError(t, decoded.Unmarshal(bz))
	require.Equal(t, original.Id, decoded.Id)
	require.Equal(t, original.Proposer, decoded.Proposer)
	require.Equal(t, original.Status, decoded.Status)
	require.NotNil(t, decoded.Content.DevelopmentSpend)
	require.True(t, decoded.Content.DevelopmentSpend.Amount.Equal(math.NewInt(42)))
	require.Equal(t, uint64(3), decoded.Content.DevelopmentSpend.Epoch)
	require.True(t, decoded.TokenNo.Equal(math.NewInt(5)))
	require.Nil(t, decoded.Content.ParameterChange)
}

func TestDefaultParamsMatchBootstrapExitStake(t *testing.T) {
	p := types.DefaultParams()
	require.NoError(t, p.Validate())
	require.True(t, p.BootstrapExitStake.Equal(math.NewInt(271_000).MulRaw(1_000_000_000)))
	require.NoError(t, types.DefaultGenesisState().Validate())
}
