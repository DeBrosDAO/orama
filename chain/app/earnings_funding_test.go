package app_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtprototypes "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app"
	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/piece"
	emissiontypes "github.com/DeBrosOfficial/network/chain/x/emission/types"
	storagekeeper "github.com/DeBrosOfficial/network/chain/x/storage/keeper"
	storagetypes "github.com/DeBrosOfficial/network/chain/x/storage/types"
	tokenkeeper "github.com/DeBrosOfficial/network/chain/x/token/keeper"
	tokentypes "github.com/DeBrosOfficial/network/chain/x/token/types"
)

// earningsOnlyChain is a committed app whose account holds earnings and no bank balance, the
// state of every provider or operator that has only been paid (C2).
type earningsOnlyChain struct {
	app  *app.OramaApp
	ctx  sdk.Context
	addr sdk.AccAddress
}

func newEarningsOnlyChain(t *testing.T, earnings int64) earningsOnlyChain {
	t.Helper()
	oramaApp := buildTestApp(t)
	genState, _ := buildGenesisState(t, oramaApp)
	stateBytes, err := json.Marshal(genState)
	require.NoError(t, err)
	genesisTime := time.Unix(1_700_000_000, 0)
	_, err = oramaApp.InitChain(&abci.RequestInitChain{ChainId: testChainID, InitialHeight: 1, Time: genesisTime, AppStateBytes: stateBytes})
	require.NoError(t, err)
	_, err = oramaApp.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: genesisTime.Add(time.Second)})
	require.NoError(t, err)
	_, err = oramaApp.Commit()
	require.NoError(t, err)

	ctx := sdk.NewContext(oramaApp.CommitMultiStore(), cmtprototypes.Header{Height: 2, Time: genesisTime.Add(2 * time.Second)}, false, oramaApp.Logger())
	addr := sdk.AccAddress(bytes.Repeat([]byte{0x42}, 20))
	coin := sdk.NewCoin(params.BaseDenom, math.NewInt(earnings))
	require.NoError(t, oramaApp.BankKeeper.MintCoins(ctx, emissiontypes.ModuleName, sdk.NewCoins(coin)))
	require.NoError(t, oramaApp.FeesKeeper.CreditEarnings(ctx, emissiontypes.ModuleName, addr, coin))
	require.True(t, oramaApp.BankKeeper.GetBalance(ctx, addr, params.BaseDenom).Amount.IsZero())
	return earningsOnlyChain{app: oramaApp, ctx: ctx, addr: addr}
}

func requireEarningsInvariant(t *testing.T, c earningsOnlyChain) {
	t.Helper()
	inv, err := c.app.FeesKeeper.CheckInvariants(c.ctx)
	require.NoError(t, err)
	require.True(t, inv.EarningsMatchModule, inv.Detail)
}

func (c earningsOnlyChain) earnings(t *testing.T) math.Int {
	t.Helper()
	got, err := c.app.FeesKeeper.GetEarnings(c.ctx, c.addr)
	require.NoError(t, err)
	return got
}

func (c earningsOnlyChain) createDealMsg(t *testing.T, epochs uint64) *storagetypes.MsgCreateDeal {
	t.Helper()
	commitment, err := piece.Commit(make([]byte, 2048))
	require.NoError(t, err)
	return &storagetypes.MsgCreateDeal{
		Signer: c.addr.String(), Class: storagetypes.DealClass_DEAL_CLASS_PUBLIC_PIN,
		DealNonce: bytes.Repeat([]byte{1}, storagetypes.NonceLen), Replicas: storagetypes.MinReplicas,
		PricePerEpoch: math.NewInt(1000), DurationEpochs: epochs,
		Pieces: []storagetypes.PieceCommitment{{
			Root: commitment.Root, RealLeafCount: commitment.RealLeafCount,
			PaddedLeafCount: commitment.PaddedLeafCount, PieceBytes: 2048,
		}},
	}
}

func TestEarningsFundOwnStorageDeal(t *testing.T) {
	c := newEarningsOnlyChain(t, 1_000_000)
	msg := c.createDealMsg(t, 2)
	msgServer := storagekeeper.NewMsgServer(c.app.StorageKeeper)
	p, err := c.app.StorageKeeper.Params.Get(c.ctx)
	require.NoError(t, err)
	need := p.DealFee.Add(msg.PricePerEpoch.MulRaw(int64(msg.Replicas)).MulRaw(int64(msg.DurationEpochs)))

	res, err := msgServer.CreateDeal(c.ctx, msg)
	require.NoError(t, err, "the handler funds the shortfall from earnings itself")
	require.NotZero(t, res.DealId)
	require.True(t, c.earnings(t).Equal(math.NewInt(1_000_000).Sub(need)), "exactly the deal fee and escrow left the earnings")
	require.True(t, c.app.BankKeeper.GetBalance(c.ctx, c.addr, params.BaseDenom).Amount.IsZero(), "the deal consumed the whole top-up")
	requireEarningsInvariant(t, c)

	extend := &storagetypes.MsgExtendDeal{Signer: c.addr.String(), DealId: res.DealId, ExtraEpochs: 3}
	before := c.earnings(t)
	_, err = msgServer.ExtendDeal(c.ctx, extend)
	require.NoError(t, err)
	extra := msg.PricePerEpoch.MulRaw(int64(msg.Replicas)).MulRaw(3)
	require.True(t, before.Sub(c.earnings(t)).Equal(extra), "an extension escrows the extra epochs from earnings")
	requireEarningsInvariant(t, c)
}

func TestEarningsLeaveOtherPeoplesDealsAlone(t *testing.T) {
	c := newEarningsOnlyChain(t, 1_000_000)
	msgServer := storagekeeper.NewMsgServer(c.app.StorageKeeper)
	msg := c.createDealMsg(t, 2)
	msg.Granter = sdk.AccAddress(bytes.Repeat([]byte{0x43}, 20)).String()
	before := c.earnings(t)
	_, err := msgServer.CreateDeal(c.ctx, msg)
	require.Error(t, err, "there is no grant")
	require.True(t, c.earnings(t).Equal(before), "a grantee spends the granter's money, never its own earnings")

	_, err = msgServer.ExtendDeal(c.ctx, &storagetypes.MsgExtendDeal{Signer: c.addr.String(), DealId: 999, ExtraEpochs: 1})
	require.Error(t, err)
	require.True(t, c.earnings(t).Equal(before), "extending a deal that does not exist moves nothing")
}

func TestEarningsFundOwnTokenCreation(t *testing.T) {
	c := newEarningsOnlyChain(t, 10_000_000_000_000)
	msg := &tokentypes.MsgCreateToken{Creator: c.addr.String(), Subdenom: "gold", Name: "Gold", Symbol: "GLD"}
	msgServer := tokenkeeper.NewMsgServerImpl(c.app.TokenKeeper)

	_, err := msgServer.CreateToken(c.ctx, msg)
	require.NoError(t, err, "the handler funds the fee and metadata deposit from earnings")
	require.True(t, c.app.BankKeeper.GetBalance(c.ctx, c.addr, params.BaseDenom).Amount.IsZero())
	requireEarningsInvariant(t, c)
}

func TestEarningsThatCannotCoverADealAreLeftUntouched(t *testing.T) {
	c := newEarningsOnlyChain(t, 10)
	_, err := storagekeeper.NewMsgServer(c.app.StorageKeeper).CreateDeal(c.ctx, c.createDealMsg(t, 2))
	require.Error(t, err)
	require.True(t, c.earnings(t).Equal(math.NewInt(10)), "a partial top-up cannot make the deal succeed, so none is made")
}
