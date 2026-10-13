package app_test

import (
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"

	housetypes "github.com/DeBrosOfficial/network/chain/x/houses/types"
)

// The token house's tally sums the delegations it reads and refuses the tally when the sum passes
// the bonded pool. Delegations to a validator that is not bonded (jailed, or too small to be bonded)
// hold tokens that are not in the bonded pool, so they must not be counted: otherwise one jailed
// validator made every tally fail, and after MaxAdvanceAttempts blocks the proposal was closed FAILED
// instead of being decided.
func TestProposalIsDecidedWhileAValidatorIsJailed(t *testing.T) {
	require.Equal(t, housetypes.ProposalStatus_REJECTED, decideEmptyProposal(t, false), "control: with every validator bonded the proposal is decided")
	require.Equal(t, housetypes.ProposalStatus_REJECTED, decideEmptyProposal(t, true), "with one jailed validator the proposal must still be decided")
}

func decideEmptyProposal(t *testing.T, jail bool) housetypes.ProposalStatus {
	oramaApp := buildTestApp(t)
	genesisTime := time.Unix(1_700_000_000, 0)
	genState, keys := committeeGenesis(t, oramaApp, 2, 365)
	initChain(t, oramaApp, genState, 0, genesisTime)
	finalize(t, oramaApp, 1, genesisTime.Add(2*time.Second))

	ctx := oramaApp.NewNextBlockContext(cmtproto.Header{Height: 2, Time: genesisTime.Add(4 * time.Second), ChainID: testChainID})
	jailedVal := sdk.ValAddress(sdk.AccAddress(keys[0].account.PubKey().Address()))
	val, err := oramaApp.StakingKeeper.GetValidator(ctx, jailedVal)
	require.NoError(t, err)
	cons, err := val.GetConsAddr()
	require.NoError(t, err)
	if jail {
		require.NoError(t, oramaApp.SlashingKeeper.Jail(ctx, cons))
	}
	proposer := sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address()).String()
	require.NoError(t, oramaApp.HousesKeeper.Proposals.Set(ctx, 1, housetypes.Proposal{
		Id: 1, Proposer: proposer, SubmitUnixNano: 1, VotingEndUnixNano: 2, Status: housetypes.ProposalStatus_VOTING,
		Content: housetypes.ProposalContent{ParameterChange: &housetypes.ParameterChange{
			TokenQuorum: housetypes.DefaultTokenQuorum, TokenPassThreshold: housetypes.DefaultTokenPassThreshold,
			VotingPeriodSeconds: int64(housetypes.DefaultVotingPeriod / time.Second), HouseBond: housetypes.DefaultHouseBond(),
			MaxEligiblePerPrefix16: 3, MaxEligiblePerAsn: 5,
		}},
		TokenYes: math.ZeroInt(), TokenNo: math.ZeroInt(), TokenAbstain: math.ZeroInt(),
	}))
	require.NoError(t, oramaApp.HousesKeeper.Active.Set(ctx, 1))
	writeCache(t, ctx)
	_, err = oramaApp.Commit()
	require.NoError(t, err)

	for h := int64(3); h <= 12; h++ {
		finalize(t, oramaApp, h, genesisTime.Add(time.Duration(2*h)*time.Second))
	}
	p, err := oramaApp.HousesKeeper.Proposals.Get(oramaApp.NewContext(true), 1)
	require.NoError(t, err)
	t.Logf("jail=%v: status %s %s", jail, p.Status, p.FailReason)
	return p.Status
}
