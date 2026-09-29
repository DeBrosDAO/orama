package keeper_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/DeBrosOfficial/network/chain/x/houses/types"
)

func setParams(t *testing.T, f *testFixture, mutate func(*types.Params)) {
	t.Helper()
	p, err := f.Keeper.Params.Get(f.Ctx)
	require.NoError(t, err)
	mutate(&p)
	require.NoError(t, p.Validate())
	require.NoError(t, f.Keeper.Params.Set(f.Ctx, p))
}

func TestTimelocks_followGenesisParams(t *testing.T) {
	f := openStructural(t)
	setParams(t, f, func(p *types.Params) {
		p.SpendTimelockSeconds = int64((9 * 24 * time.Hour) / time.Second)
	})
	f.Emission.ceiling[1] = math.NewInt(100)
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), spendContent(math.NewInt(10)))
	require.NoError(t, err)
	f.passStructuralVotes(t, id)
	f.advance(t, 24*time.Hour)
	require.Equal(t, types.ProposalStatus_TIMELOCK, f.proposal(t, id).Status)

	f.advance(t, 9*24*time.Hour-time.Second)
	require.Equal(t, types.ProposalStatus_TIMELOCK, f.proposal(t, id).Status, "the 7-day default must not apply")
	require.Zero(t, f.Emission.calls)
	f.advance(t, time.Second)
	require.Equal(t, types.ProposalStatus_EXECUTED, f.proposal(t, id).Status)
}

func TestVetoWindow_followsGenesisParams(t *testing.T) {
	f := openParameter(t)
	setParams(t, f, func(p *types.Params) {
		p.VetoWindowSeconds = int64((10 * 24 * time.Hour) / time.Second)
	})
	id := f.submitParameter(t)
	f.advance(t, 24*time.Hour)
	require.Equal(t, types.ProposalStatus_VETO_WINDOW, f.proposal(t, id).Status)
	f.advance(t, 10*24*time.Hour-time.Second)
	require.Equal(t, types.ProposalStatus_VETO_WINDOW, f.proposal(t, id).Status)
	f.advance(t, time.Second)
	require.Equal(t, types.ProposalStatus_TIMELOCK, f.proposal(t, id).Status)
}

func TestMinHouseSize_followsGenesisParams(t *testing.T) {
	f := newTestFixture(t)
	setParams(t, f, func(p *types.Params) { p.MinHouseSize = 25 })
	f.Power.lambda = math.LegacyOneDec()
	f.seatHouse(t, 21)
	view := mustTiers(t, f)
	require.Equal(t, 21, view.Eligible)
	require.False(t, view.Parameter, "21 operators no longer fill a house of 25")

	setParams(t, f, func(p *types.Params) { p.MinHouseSize = 21 })
	require.True(t, mustTiers(t, f).Parameter)
}

func TestSoftwareUpgrade_isScheduledOnlyAfterItsTimelock(t *testing.T) {
	f := openStructural(t)
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{SoftwareUpgrade: &types.SoftwareUpgrade{Name: "v2", Height: 5_000}})
	require.NoError(t, err)
	f.passStructuralVotes(t, id)
	f.advance(t, 24*time.Hour)
	f.advance(t, 60*24*time.Hour-time.Second)
	require.Empty(t, f.Upgrades.plans, "nothing is scheduled before the 60-day timelock ends")
	require.Nil(t, mustEnacted(t, f).ScheduledUpgrade)

	f.advance(t, time.Second)
	require.Equal(t, types.ProposalStatus_EXECUTED, f.proposal(t, id).Status)
	require.Equal(t, []plan{{name: "v2", height: 5_000}}, f.Upgrades.plans)
	require.Equal(t, "v2", mustEnacted(t, f).ScheduledUpgrade.Name)
}

func TestSoftwareUpgrade_failsWhenSchedulerRefusesAndLeavesNoState(t *testing.T) {
	f := openStructural(t)
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{SoftwareUpgrade: &types.SoftwareUpgrade{Name: "v2", Height: 5_000}})
	require.NoError(t, err)
	f.passStructuralVotes(t, id)
	f.advance(t, 24*time.Hour)
	// The chain has passed the planned height while the timelock ran.
	f.Ctx = f.Ctx.WithBlockHeight(6_000)
	f.advance(t, 60*24*time.Hour)

	p := f.proposal(t, id)
	require.Equal(t, types.ProposalStatus_FAILED, p.Status)
	require.NotEmpty(t, p.FailReason)
	require.Empty(t, f.Upgrades.plans)
	require.Nil(t, mustEnacted(t, f).ScheduledUpgrade)
}

func TestSoftwareUpgrade_failsWithoutAScheduler(t *testing.T) {
	f := openStructural(t)
	f.Keeper = f.Keeper.WithEnactors(f.Reporters, nil)
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{SoftwareUpgrade: &types.SoftwareUpgrade{Name: "v2", Height: 5_000}})
	require.NoError(t, err)
	f.passStructuralVotes(t, id)
	f.advance(t, 24*time.Hour)
	f.advance(t, 60*24*time.Hour)

	p := f.proposal(t, id)
	require.Equal(t, types.ProposalStatus_FAILED, p.Status)
	require.Contains(t, p.FailReason, "no upgrade scheduler")
	require.Nil(t, mustEnacted(t, f).ScheduledUpgrade)
}

func TestRelayReporters_changeAfterTimelockOnly(t *testing.T) {
	f := openStructural(t)
	f.Reporters.set[acc(300).String()] = true
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{RelayReporters: &types.RelayReporterChange{
		Add:    []string{acc(301).String()},
		Remove: []string{acc(300).String()},
	}})
	require.NoError(t, err)
	f.passStructuralVotes(t, id)
	f.advance(t, 24*time.Hour)
	f.advance(t, 60*24*time.Hour-time.Second)
	require.Zero(t, f.Reporters.changes)

	f.advance(t, time.Second)
	require.Equal(t, types.ProposalStatus_EXECUTED, f.proposal(t, id).Status)
	require.Equal(t, map[string]bool{acc(301).String(): true}, f.Reporters.set)
	require.Equal(t, []string{acc(301).String()}, mustEnacted(t, f).RelayReporters)
}

func TestRelayReporters_refusedChangeFailsTheProposal(t *testing.T) {
	f := openStructural(t)
	f.Reporters.fail = true
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{RelayReporters: &types.RelayReporterChange{
		Remove: []string{acc(300).String()},
	}})
	require.NoError(t, err)
	f.passStructuralVotes(t, id)
	f.advance(t, 24*time.Hour)
	f.advance(t, 60*24*time.Hour)

	require.Equal(t, types.ProposalStatus_FAILED, f.proposal(t, id).Status)
	require.Empty(t, mustEnacted(t, f).RelayReporters)
}

func TestRelayReporters_failsWithoutARelayKeeper(t *testing.T) {
	f := openStructural(t)
	f.Keeper = f.Keeper.WithEnactors(nil, f.Upgrades)
	id, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{RelayReporters: &types.RelayReporterChange{
		Add: []string{acc(301).String()},
	}})
	require.NoError(t, err)
	f.passStructuralVotes(t, id)
	f.advance(t, 24*time.Hour)
	f.advance(t, 60*24*time.Hour)
	require.Equal(t, types.ProposalStatus_FAILED, f.proposal(t, id).Status)
}

func TestEnactedReaders_reflectPassedProposals(t *testing.T) {
	f := openStructural(t)
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	split, err := f.Keeper.EnactedEmissionSplit(f.Ctx)
	require.NoError(t, err)
	require.Nil(t, split, "no split is enacted at genesis")
	allowed, err := f.Keeper.CodeUploadAllowed(f.Ctx, hash)
	require.NoError(t, err)
	require.False(t, allowed)

	splitID, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{EmissionSplit: &types.EmissionSplitChange{
		ValidatorPercent: 70, StoragePercent: 15, RelayPercent: 10, DevelopmentPercent: 5,
	}})
	require.NoError(t, err)
	f.passStructuralVotes(t, splitID)
	listID, err := f.Keeper.SubmitProposal(f.Ctx, acc(200), types.ProposalContent{AllowList: &types.AllowListChange{
		CodeUploadAdd: []string{hash},
		AdapterAdd:    []string{"ibc-bridge-1"},
	}})
	require.NoError(t, err)
	f.passStructuralVotes(t, listID)
	f.advance(t, 24*time.Hour)
	f.advance(t, 60*24*time.Hour)

	split, err = f.Keeper.EnactedEmissionSplit(f.Ctx)
	require.NoError(t, err)
	require.NotNil(t, split)
	require.Equal(t, uint32(70), split.ValidatorPercent)
	allowed, err = f.Keeper.CodeUploadAllowed(f.Ctx, hash)
	require.NoError(t, err)
	require.True(t, allowed)
	adapter, err := f.Keeper.AdapterAllowed(f.Ctx, "ibc-bridge-1")
	require.NoError(t, err)
	require.True(t, adapter)
	other, err := f.Keeper.CodeUploadAllowed(f.Ctx, "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	require.NoError(t, err)
	require.False(t, other)
}
