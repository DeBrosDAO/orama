package app_test

import (
	stded25519 "crypto/ed25519"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/DeBrosOfficial/network/chain/app/params"

	nodeskeeper "github.com/DeBrosOfficial/network/chain/x/nodes/keeper"
	nodestypes "github.com/DeBrosOfficial/network/chain/x/nodes/types"
	relaykeeper "github.com/DeBrosOfficial/network/chain/x/relay/keeper"
	relaytypes "github.com/DeBrosOfficial/network/chain/x/relay/types"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
)

// addRelayNodeFor registers another RELAY node under the operator that owns first.
func (c *wiringChain) addRelayNodeFor(first relayNode, id, endpoint string) relayNode {
	c.t.Helper()
	n := relayNode{id: id, operator: first.operator}
	hotPriv := secp256k1.GenPrivKey()
	hot := sdk.AccAddress(hotPriv.PubKey().Address())
	hotSig, err := hotPriv.Sign(nodestypes.BindingSignBytes(testChainID, n.operator.String(), nodestypes.HotKeyService, hotPriv.PubKey().Bytes()))
	require.NoError(c.t, err)
	pub, priv, err := stded25519.GenerateKey(nil)
	require.NoError(c.t, err)
	n.priv = priv
	c.write(func(ctx sdk.Context) {
		_, err := nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper).RegisterNode(ctx, &nodestypes.MsgRegisterNode{
			Operator: n.operator.String(), NodeId: id, Roles: []nodestypes.Role{nodestypes.RoleRelay}, HotKey: hot.String(),
			Bindings: []nodestypes.Binding{{
				Service: nodestypes.HotKeyService, KeyType: nodestypes.KeyTypeSecp256k1, Pubkey: hotPriv.PubKey().Bytes(), Signature: hotSig,
			}, {
				Service: "relay", KeyType: nodestypes.KeyTypeEd25519, Pubkey: pub,
				Signature: stded25519.Sign(priv, nodestypes.BindingSignBytes(testChainID, n.operator.String(), "relay", pub)),
			}},
			Endpoints: []string{endpoint},
		})
		require.NoError(c.t, err)
	})
	return n
}

// relayVote is what one reporter observed of one relay.
type relayVote struct {
	node   relayNode
	fp     []byte
	weight int64
	uptime string
	flags  uint32
}

func (v relayVote) observation() relaytypes.RelayObservation {
	return relaytypes.RelayObservation{
		RsaFingerprint:  v.fp,
		Ed25519Id:       v.node.priv.Public().(stded25519.PublicKey),
		ConsensusWeight: math.NewInt(v.weight),
		Flags:           v.flags,
		UptimeFraction:  math.LegacyMustNewDecFromStr(v.uptime),
	}
}

// reportVotes has every reporter report its own votes for the epoch that just closed, and returns it.
func (c *wiringChain) reportVotes(reporters []sdk.AccAddress, votes [][]relayVote) uint64 {
	c.t.Helper()
	require.Len(c.t, votes, len(reporters))
	var epoch uint64
	c.write(func(ctx sdk.Context) {
		current, err := c.app.EmissionKeeper.CurrentEpoch(ctx)
		require.NoError(c.t, err)
		epoch = current - 1
		for i, reporter := range reporters {
			entries := make([]relaytypes.RelayObservation, len(votes[i]))
			for j, v := range votes[i] {
				entries[j] = v.observation()
			}
			root, err := relaytypes.InputsRoot(entries)
			require.NoError(c.t, err)
			_, err = relaykeeper.NewMsgServerImpl(c.app.RelayKeeper).ReportEpoch(ctx, &relaytypes.MsgReportEpoch{
				Reporter: reporter.String(), Epoch: epoch, ChunkCount: 1, Entries: entries, InputsRoot: root,
			})
			require.NoError(c.t, err)
		}
	})
	return epoch
}

func (c *wiringChain) earningsOf(addr sdk.AccAddress) math.Int {
	c.t.Helper()
	earned, err := c.app.FeesKeeper.GetEarnings(c.app.NewContext(true), addr)
	require.NoError(c.t, err)
	return earned
}

func newReporters(c *wiringChain) []sdk.AccAddress {
	c.t.Helper()
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
	return reporters
}

// everyReporterSees gives each of the three reporters the same votes, except where a test says otherwise.
func everyReporterSees(votes []relayVote) [][]relayVote { return [][]relayVote{votes, votes, votes} }

// settle runs the blocks that carry an epoch past its report window, so it settles.
func (c *wiringChain) settle() { c.blocks(int(relaytypes.ReportWindowEpochs) + 2) }

// TestApp_aRelayEpochPaysEachOperatorFromTheMedianOfTheReporters drives a full relay epoch through
// FinalizeBlock with three reporters and six relays of five operators, and checks every operator's
// earnings against the rules: the median of the reporters' weights (one inflated report moves
// nothing), the per-relay cap, the exit multiplier, the uptime floor, an operator's relays summed,
// and a retired node paid nothing. The split of what is minted is 90% to the operator's earnings, 5%
// burned and 5% to the archive fund, and every module invariant holds afterwards.
func TestApp_aRelayEpochPaysEachOperatorFromTheMedianOfTheReporters(t *testing.T) {
	c := newWiringChain(t)
	nodeA := c.addRelayNode("relay-a", "https://93.184.216.34:443")
	nodeC := c.addRelayNodeFor(nodeA, "relay-c", "https://93.184.216.35:443")
	nodeB := c.addRelayNode("relay-b", "https://151.101.2.10:443")
	nodeD := c.addRelayNode("relay-d", "https://45.33.100.10:443")
	nodeE := c.addRelayNode("relay-e", "https://142.250.1.10:443")
	nodeF := c.addRelayNode("relay-f", "https://104.16.1.10:443")
	fpA, fpC, fpB := c.registerRelay(nodeA, 0xA1), c.registerRelay(nodeC, 0xC1), c.registerRelay(nodeB, 0xB1)
	fpD, fpE, fpF := c.registerRelay(nodeD, 0xD1), c.registerRelay(nodeE, 0xE1), c.registerRelay(nodeF, 0xF1)
	reporters := newReporters(c)

	const (
		median       = 2_000_000       // A: reporters say 1,000,000 / 2,000,000 / 900,000,000,000
		sameOperator = 4_000_000       // C: the second relay of A's operator
		exitWeight   = 3_000_000       // B: doubled for an exit
		capped       = 100_000_000_000 // E: reported 150,000,000,000, per_relay_cap is 100 ORAMA
	)
	votes := func() [][]relayVote {
		a := func(weight int64) relayVote { return relayVote{nodeA, fpA, weight, "1", 0} }
		rest := []relayVote{
			{nodeC, fpC, sameOperator, "1", 0},
			{nodeB, fpB, exitWeight, "1", relaytypes.FlagExit},
			{nodeD, fpD, 5_000_000, "0.5", 0},
			{nodeE, fpE, 150_000_000_000, "1", 0},
			{nodeF, fpF, 7_000_000, "1", 0},
		}
		return [][]relayVote{
			append([]relayVote{a(1_000_000)}, rest...),
			append([]relayVote{a(2_000_000)}, rest...),
			append([]relayVote{a(900_000_000_000)}, rest...),
		}
	}

	// The first epoch with quorum only activates rewards.
	first := c.reportVotes(reporters, votes())
	c.settle()
	activation, err := c.app.RelayKeeper.EpochResults.Get(c.app.NewContext(true), first)
	require.NoError(t, err)
	require.True(t, activation.Activating)
	require.True(t, c.earningsOf(nodeA.operator).IsZero(), "an activating epoch pays nothing")

	// Node F leaves before the second epoch is settled.
	second := c.reportVotes(reporters, votes())
	c.write(func(ctx sdk.Context) {
		_, err := nodeskeeper.NewMsgServerImpl(c.app.NodesKeeper).RetireNode(ctx, &nodestypes.MsgRetireNode{Operator: nodeF.operator.String(), NodeId: nodeF.id})
		require.NoError(t, err)
	})
	// Retiring refunded F's state deposit to its earnings; that is not relay pay.
	retiredBefore := c.earningsOf(nodeF.operator)
	c.settle()

	result, err := c.app.RelayKeeper.EpochResults.Get(c.app.NewContext(true), second)
	require.NoError(t, err)
	operatorA := int64(median + sameOperator)
	exitB := int64(exitWeight * 2)
	wantMinted := operatorA + exitB + capped
	require.True(t, result.Minted.Equal(math.NewInt(wantMinted)), "minted %s, want %d", result.Minted, wantMinted)

	// 90% of each operator's total reaches its earnings account.
	require.Equal(t, "5400000", c.earningsOf(nodeA.operator).String(), "operator of A and C: the median 2,000,000 plus 4,000,000")
	require.Equal(t, "5400000", c.earningsOf(nodeB.operator).String(), "operator of B: an exit pays twice its weight")
	require.Equal(t, "90000000000", c.earningsOf(nodeE.operator).String(), "operator of E: capped at per_relay_cap")
	require.True(t, c.earningsOf(nodeD.operator).IsZero(), "a relay under the uptime floor is not paid")
	require.True(t, c.earningsOf(nodeF.operator).Equal(retiredBefore), "a retired node's relay is not paid")

	fund, err := c.app.StorageKeeper.ArchiveFund.Get(c.app.NewContext(true))
	require.NoError(t, err)
	require.Equal(t, "5000600000", fund.String(), "5% of each operator's total funds the archive")
	c.requireModuleInvariants()
	ctx := c.app.NewContext(true)
	archiveBalance := c.app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(storagetypes.ArchiveModuleName), params.BaseDenom)
	require.True(t, archiveBalance.Amount.Equal(fund), "the archive account holds exactly the fund counter")
	relayBalance := c.app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(relaytypes.ModuleName), params.BaseDenom)
	require.True(t, relayBalance.Amount.IsZero(), "settlement leaves nothing in the relay account: 90% went to earnings, 5% to the archive and 5% was burned")
	inv, err := c.app.RelayKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, inv.CeilingHolds && inv.PayoutsMatch, inv.Detail)
	feesInv, err := c.app.FeesKeeper.CheckInvariants(ctx)
	require.NoError(t, err)
	require.True(t, feesInv.EarningsMatchModule && feesInv.DepositsMatchModule && feesInv.FeesBalance, feesInv.Detail)
}
