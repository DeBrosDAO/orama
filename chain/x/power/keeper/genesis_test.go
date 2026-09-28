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
