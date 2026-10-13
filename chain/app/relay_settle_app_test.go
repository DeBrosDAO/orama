package app_test

import (
	stded25519 "crypto/ed25519"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"

	relaykeeper "github.com/DeBrosOfficial/network/chain/x/relay/keeper"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
)

// reportClosedEpoch sends every reporter's report for the epoch that just closed and
// returns that epoch.
func (c *wiringChain) reportClosedEpoch(reporters []sdk.AccAddress, n relayNode, fp []byte, weight int64) uint64 {
	c.t.Helper()
	var epoch uint64
	c.write(func(ctx sdk.Context) {
		current, err := c.app.EmissionKeeper.CurrentEpoch(ctx)
		require.NoError(c.t, err)
		epoch = current - 1
		entries := []relaytypes.RelayObservation{{
			RsaFingerprint:  fp,
			Ed25519Id:       n.priv.Public().(stded25519.PublicKey),
			ConsensusWeight: math.NewInt(weight),
			UptimeFraction:  math.LegacyOneDec(),
		}}
		root, err := relaytypes.InputsRoot(entries)
		require.NoError(c.t, err)
		for _, reporter := range reporters {
			_, err := relaykeeper.NewMsgServerImpl(c.app.RelayKeeper).ReportEpoch(ctx, &relaytypes.MsgReportEpoch{
				Reporter: reporter.String(), Epoch: epoch, ChunkCount: 1, Entries: entries, InputsRoot: root,
			})
			require.NoError(c.t, err)
		}
	})
	return epoch
}

// Before x/relay had an end block nothing ever called SettleEpoch, so a report sat in state
// forever and no relay was paid. Here three reporters (the default quorum) report two epochs through the real app and
// only finalized blocks run: the first epoch activates rewards, the second mints against its
// ceiling and credits the relay operator's earnings account.
func TestApp_relayEpochsSettleWithoutAnyMessage(t *testing.T) {
	c := newWiringChain(t)
	node := c.addRelayNode("relay-settle", "https://93.184.216.34:443")
	fp := c.registerRelay(node, 0x33)
	reporters := []sdk.AccAddress{
		sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address()),
		sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address()),
		sdk.AccAddress(ed25519.GenPrivKey().PubKey().Address()),
	}
	c.write(func(ctx sdk.Context) {
		_, err := relaykeeper.NewMsgServerImpl(c.app.RelayKeeper).UpdateReporters(relaykeeper.WithAllowReporterChange(ctx),
			&relaytypes.MsgUpdateReporters{Signer: reporters[0].String(), Reporters: []string{reporters[0].String(), reporters[1].String(), reporters[2].String()}})
		require.NoError(c.t, err)
	})

	first := c.reportClosedEpoch(reporters, node, fp, 100_000)
	c.blocks(int(relaytypes.ReportWindowEpochs) + 2)
	activation, err := c.app.RelayKeeper.EpochResults.Get(c.app.NewContext(true), first)
	require.NoError(t, err)
	require.True(t, activation.Activating, "the first epoch with quorum activates rewards")

	second := c.reportClosedEpoch(reporters, node, fp, 100_000)
	c.blocks(int(relaytypes.ReportWindowEpochs) + 2)
	paid, err := c.app.RelayKeeper.EpochResults.Get(c.app.NewContext(true), second)
	require.NoError(t, err)
	require.True(t, paid.Minted.IsPositive(), "the second epoch mints against its relay ceiling")

	earned, err := c.app.FeesKeeper.GetEarnings(c.app.NewContext(true), node.operator)
	require.NoError(t, err)
	require.True(t, paid.Minted.Equal(math.NewInt(100_000)), "minted %s", paid.Minted)
	// The C2 service split: 90% to the operator's earnings, 5% burned, 5% to the archive fund.
	require.True(t, earned.Equal(math.NewInt(90_000)), "earnings %s", earned)
	fund, err := c.app.StorageKeeper.ArchiveFund.Get(c.app.NewContext(true))
	require.NoError(t, err)
	require.True(t, fund.Equal(math.NewInt(5_000)), "archive fund %s", fund)
	ctx := c.app.NewContext(true)
	archiveBal := c.app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(storagetypes.ArchiveModuleName), params.BaseDenom)
	require.True(t, archiveBal.Amount.Equal(fund), "archive module holds %s, fund counter %s", archiveBal.Amount, fund)
	relayBal := c.app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(relaytypes.ModuleName), params.BaseDenom)
	require.True(t, relayBal.Amount.IsZero(), "the relay module account is emptied by settlement, holds %s", relayBal.Amount)
	c.requireModuleInvariants()
	inv, err := c.app.RelayKeeper.CheckInvariants(c.app.NewContext(true))
	require.NoError(t, err)
	require.True(t, inv.CeilingHolds && inv.PayoutsMatch, inv.Detail)
}
