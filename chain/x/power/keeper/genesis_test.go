package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

func threeMemberCommittee(t *testing.T) []types.BootstrapMember {
	t.Helper()
	addrs := []sdk.AccAddress{
		sdk.AccAddress("committee_member_one_"),
		sdk.AccAddress("committee_member_two_"),
		sdk.AccAddress("committee_member_thre"),
	}
	members := make([]types.BootstrapMember, len(addrs))
	for i, a := range addrs {
		members[i] = types.BootstrapMember{
			OperatorAddress: a.String(),
			Moniker:         "member",
			ConsensusPubkey: testPubKey(byte(i + 1)),
		}
	}
	return members
}

func TestInitGenesis_committeeSizeGate_devnetAllowsSmallCommittee(t *testing.T) {
	f := newTestFixture(t)
	f.Ctx = f.Ctx.WithChainID("orama-devnet-1")
	committee := threeMemberCommittee(t)

	gs := types.DefaultGenesisState()
	gs.Params.MinCommitteeSize = 1
	gs.BootstrapCommittee = committee
	updates, err := f.Keeper.InitGenesis(f.Ctx, *gs, f.Emission)
	require.NoError(t, err)
	require.Len(t, updates, 3)
}

func TestInitGenesis_committeeSizeGate_rejectsUndersizedProductionCommittee(t *testing.T) {
	f := newTestFixture(t)
	f.Ctx = f.Ctx.WithChainID("orama-1")
	committee := threeMemberCommittee(t)

	gs := types.DefaultGenesisState()
	gs.Params.MinCommitteeSize = 1
	gs.BootstrapCommittee = committee
	_, err := f.Keeper.InitGenesis(f.Ctx, *gs, f.Emission)
	require.Error(t, err)
}

func TestInitGenesis_equalPowerForEachCommitteeMember(t *testing.T) {
	f := newTestFixture(t)
	f.Ctx = f.Ctx.WithChainID("orama-devnet-1")
	committee := threeMemberCommittee(t)

	gs := types.DefaultGenesisState()
	gs.Params.MinCommitteeSize = 1
	gs.BootstrapCommittee = committee
	updates, err := f.Keeper.InitGenesis(f.Ctx, *gs, f.Emission)
	require.NoError(t, err)
	require.Len(t, updates, 3)

	// 1/3 share -> floor(0.333... * 1e9) = 333,333,333 for every member (equal split, no
	// remainder-to-one-member bias at the genesis power-assignment step).
	for _, u := range updates {
		require.Equal(t, int64(333_333_333), u.Power)
	}
}

func TestInitGenesis_rejectsWrongLengthConsensusPubkey(t *testing.T) {
	f := newTestFixture(t)
	f.Ctx = f.Ctx.WithChainID("orama-devnet-1")
	gs := types.DefaultGenesisState()
	gs.Params.MinCommitteeSize = 1
	gs.BootstrapCommittee = []types.BootstrapMember{{
		OperatorAddress: sdk.AccAddress("short_pubkey_member_").String(),
		ConsensusPubkey: []byte{1, 2, 3},
	}}
	_, err := f.Keeper.InitGenesis(f.Ctx, *gs, f.Emission)
	require.Error(t, err)
}

func TestExportGenesis_roundTrip(t *testing.T) {
	f := newTestFixture(t)
	f.Ctx = f.Ctx.WithChainID("orama-devnet-1")
	committee := threeMemberCommittee(t)
	gs := types.DefaultGenesisState()
	gs.Params.MinCommitteeSize = 1
	gs.BootstrapCommittee = committee
	_, err := f.Keeper.InitGenesis(f.Ctx, *gs, f.Emission)
	require.NoError(t, err)

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.Len(t, exported.BootstrapCommittee, 3)
	require.Len(t, exported.PowerRecords, 3)
	require.True(t, exported.Lambda.Equal(math.LegacyZeroDec()))
	require.NoError(t, exported.Validate())
}

func TestExportGenesis_rampBondRoundTrip(t *testing.T) {
	f := newTestFixture(t)
	f.Ctx = f.Ctx.WithChainID("orama-devnet-1")
	committee := threeMemberCommittee(t)
	gs := types.DefaultGenesisState()
	gs.Params.MinCommitteeSize = 1
	gs.BootstrapCommittee = committee
	_, err := f.Keeper.InitGenesis(f.Ctx, *gs, f.Emission)
	require.NoError(t, err)

	acc, err := sdk.AccAddressFromBech32(committee[0].OperatorAddress)
	require.NoError(t, err)
	valoper := sdk.ValAddress(acc).String()
	require.NoError(t, f.Keeper.RampActivation.Set(f.Ctx, valoper, 2))
	require.NoError(t, f.Keeper.RampAdmitted.Set(f.Ctx, valoper, math.NewInt(1_000)))
	require.NoError(t, f.Keeper.RampExcess.Set(f.Ctx, valoper, math.NewInt(250)))
	require.NoError(t, f.Keeper.RampExcessEpoch.Set(f.Ctx, valoper, 4))

	exported, err := f.Keeper.ExportGenesis(f.Ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())

	restored := newTestFixture(t)
	restored.Ctx = restored.Ctx.WithChainID("orama-devnet-1")
	_, err = restored.Keeper.InitGenesis(restored.Ctx, *exported, restored.Emission)
	require.NoError(t, err)
	activation, err := restored.Keeper.RampActivation.Get(restored.Ctx, valoper)
	require.NoError(t, err)
	require.Equal(t, uint64(2), activation)
	admitted, err := restored.Keeper.RampAdmitted.Get(restored.Ctx, valoper)
	require.NoError(t, err)
	require.True(t, admitted.Equal(math.NewInt(1_000)))
	excess, err := restored.Keeper.RampExcess.Get(restored.Ctx, valoper)
	require.NoError(t, err)
	require.True(t, excess.Equal(math.NewInt(250)))
	excessEpoch, err := restored.Keeper.RampExcessEpoch.Get(restored.Ctx, valoper)
	require.NoError(t, err)
	require.Equal(t, uint64(4), excessEpoch)
}
