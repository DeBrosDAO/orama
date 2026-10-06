package keeper_test

import (
	"bytes"
	"context"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	"github.com/DeBrosOfficial/network/chain/x/cnft/keeper"
	"github.com/DeBrosOfficial/network/chain/x/cnft/types"
)

func init() {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
}

type lockedDeposit struct {
	owner  sdk.AccAddress
	id     string
	amount math.Int
}

type fakeFees struct {
	locks []lockedDeposit
}

func (f *fakeFees) LockDeposit(_ context.Context, owner sdk.AccAddress, id string, amount math.Int) error {
	f.locks = append(f.locks, lockedDeposit{owner: owner, id: id, amount: amount})
	return nil
}

type spyEarnings struct {
	calls int
}

func (s *spyEarnings) CreditEarnings(context.Context, string, sdk.AccAddress, sdk.Coin) error {
	s.calls++
	return nil
}

type fixture struct {
	ctx      sdk.Context
	keeper   keeper.Keeper
	fees     *fakeFees
	earnings *spyEarnings
	msg      types.MsgServer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	fees := &fakeFees{}
	earnings := &spyEarnings{}
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), fees, earnings)
	require.NoError(t, k.InitGenesis(testCtx.Ctx, *types.DefaultGenesisState()))
	return &fixture{
		ctx:      testCtx.Ctx,
		keeper:   k,
		fees:     fees,
		earnings: earnings,
		msg:      keeper.NewMsgServer(k),
	}
}

func bech(n byte) string {
	raw := make(sdk.AccAddress, 20)
	for i := range raw {
		raw[i] = n
	}
	return raw.String()
}

func assetID(n byte) []byte {
	id := bytes.Repeat([]byte{n}, types.HashSize)
	return id
}

func (f *fixture) createCollection(t *testing.T, creator string, bps uint32) uint64 {
	t.Helper()
	res, err := f.msg.CreateCollection(f.ctx, &types.MsgCreateCollection{
		Creator: creator, Name: "demo", RoyaltyBps: bps,
	})
	require.NoError(t, err)
	return res.Id
}

func (f *fixture) createTree(t *testing.T, creator string, collection uint64, depth, buffer, canopy uint32) (uint64, []byte) {
	t.Helper()
	res, err := f.msg.CreateTree(f.ctx, &types.MsgCreateTree{
		Creator: creator, CollectionId: collection, Depth: depth, Buffer: buffer, Canopy: canopy,
	})
	require.NoError(t, err)
	return res.Id, res.Root
}
