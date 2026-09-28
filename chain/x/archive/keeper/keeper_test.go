package keeper_test

import (
	"bytes"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/archive/keeper"
	"github.com/DeBrosOfficial/network/chain/x/archive/types"
)

type testFixture struct {
	Ctx    sdk.Context
	Keeper keeper.Keeper
	Msg    types.MsgServer
	Query  types.QueryServer
}

func newTestFixture(t *testing.T) *testFixture {
	t.Helper()

	key := storetypes.NewKVStoreKey(types.StoreKey)
	tkey := storetypes.NewTransientStoreKey("transient_test")
	testCtx := testutil.DefaultContextWithDB(t, key, tkey)
	ctx := testCtx.Ctx.WithBlockHeader(cmtproto.Header{
		Height: 1_000_000,
		Time:   time.Unix(1_700_000_000, 0),
	}).WithBlockHeight(1_000_000)

	interfaceRegistry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(interfaceRegistry)
	cdc := codec.NewProtoCodec(interfaceRegistry)
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key))

	return &testFixture{
		Ctx:    ctx,
		Keeper: k,
		Msg:    keeper.NewMsgServerImpl(k),
		Query:  keeper.NewQueryServerImpl(k),
	}
}

func (f *testFixture) initGenesis(t *testing.T, mutate func(*types.GenesisState)) {
	t.Helper()
	gs := types.DefaultGenesisState()
	if mutate != nil {
		mutate(gs)
	}
	require.NoError(t, f.Keeper.InitGenesis(f.Ctx, *gs))
}

func acc(n byte) sdk.AccAddress {
	return sdk.AccAddress(bytes.Repeat([]byte{n}, 20))
}

func digest(b byte) []byte {
	return bytes.Repeat([]byte{b}, types.HashLen)
}

func (f *testFixture) attest(t *testing.T, signer byte, start, end int64, cid string, bundle, root []byte) *types.MsgAttestResponse {
	t.Helper()
	res, err := f.Msg.Attest(f.Ctx, &types.MsgAttest{
		Archiver:    acc(signer).String(),
		StartHeight: start,
		EndHeight:   end,
		BundleCid:   cid,
		BundleHash:  bundle,
		MerkleRoot:  root,
	})
	require.NoError(t, err)
	return res
}

func (f *testFixture) attach(t *testing.T, signer byte, start, end int64, dealIDs ...string) *types.MsgAttachReplicasResponse {
	t.Helper()
	res, err := f.Msg.AttachReplicas(f.Ctx, &types.MsgAttachReplicas{
		Archiver:    acc(signer).String(),
		StartHeight: start,
		EndHeight:   end,
		DealIds:     dealIDs,
	})
	require.NoError(t, err)
	return res
}
