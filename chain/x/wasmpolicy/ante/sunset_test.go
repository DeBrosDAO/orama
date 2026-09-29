package ante_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	protov2 "google.golang.org/protobuf/proto"

	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/ante"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/keeper"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

const sunsetHeight uint64 = 100

func newSunset(t *testing.T, codeIDs ...uint64) (ante.UploadSunsetDecorator, sdk.Context, keeper.Keeper) {
	t.Helper()
	key := storetypes.NewKVStoreKey(types.StoreKey)
	ctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_wasmpolicy")).Ctx
	k := keeper.NewKeeper(runtime.NewKVStoreService(key), nil)
	gs := types.DefaultGenesisState()
	gs.UploadSunsetHeight = sunsetHeight
	gs.GenesisCodeIDs = codeIDs
	require.NoError(t, k.InitGenesis(ctx, gs))
	return ante.NewUploadSunsetDecorator(k), ctx, k
}

type txOf struct{ msgs []sdk.Msg }

func (t txOf) GetMsgs() []sdk.Msg                    { return t.msgs }
func (t txOf) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }

type protoMsg struct{}

func (protoMsg) Reset()         {}
func (protoMsg) String() string { return "" }
func (protoMsg) ProtoMessage()  {}

type storeMsg struct {
	protoMsg
	id    uint64
	store bool
}

func (m storeMsg) GetStoreCodeID() (uint64, bool) { return m.id, m.store }

type wasmdStoreMsg struct{ protoMsg }

func (wasmdStoreMsg) XXX_MessageName() string { return "cosmwasm.wasm.v1.MsgStoreCode" }

type sunsetMsg struct {
	protoMsg
	height uint64
}

func (m sunsetMsg) ProposedUploadSunset() (uint64, bool) { return m.height, true }

func TestUploadSunset(t *testing.T) {
	decorator, ctx, k := newSunset(t, 7)
	nextOK := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }

	t.Run("before height rejects a code id outside the genesis set", func(t *testing.T) {
		_, err := decorator.AnteHandle(ctx.WithBlockHeight(99), txOf{msgs: []sdk.Msg{storeMsg{id: 8, store: true}}}, false, nextOK)
		require.ErrorIs(t, err, types.ErrUploadClosed)
		_, err = decorator.AnteHandle(ctx.WithBlockHeight(99), txOf{msgs: []sdk.Msg{wasmdStoreMsg{}}}, false, nextOK)
		require.ErrorIs(t, err, types.ErrUploadClosed)
	})

	t.Run("before height allows a code id in the genesis set", func(t *testing.T) {
		_, err := decorator.AnteHandle(ctx.WithBlockHeight(99), txOf{msgs: []sdk.Msg{storeMsg{id: 7, store: true}}}, false, nextOK)
		require.NoError(t, err)
	})

	t.Run("at the height store is allowed", func(t *testing.T) {
		_, err := decorator.AnteHandle(ctx.WithBlockHeight(int64(sunsetHeight)), txOf{msgs: []sdk.Msg{storeMsg{id: 9, store: true}}}, false, nextOK)
		require.NoError(t, err)
		_, err = decorator.AnteHandle(ctx.WithBlockHeight(int64(sunsetHeight)+1), txOf{msgs: []sdk.Msg{wasmdStoreMsg{}}}, true, nextOK)
		require.NoError(t, err)
	})

	t.Run("no message can change the height", func(t *testing.T) {
		before, err := k.ExportGenesis(ctx)
		require.NoError(t, err)
		_, err = decorator.AnteHandle(ctx.WithBlockHeight(1), txOf{msgs: []sdk.Msg{sunsetMsg{height: 1}}}, false, nextOK)
		require.ErrorIs(t, err, types.ErrSunsetImmutable)
		after, err := k.ExportGenesis(ctx)
		require.NoError(t, err)
		require.Equal(t, before.UploadSunsetHeight, after.UploadSunsetHeight)
		second := types.DefaultGenesisState()
		second.UploadSunsetHeight = 1
		require.ErrorIs(t, k.InitGenesis(ctx, second), types.ErrSunsetImmutable)
		after, err = k.ExportGenesis(ctx)
		require.NoError(t, err)
		require.Equal(t, sunsetHeight, after.UploadSunsetHeight)
	})
}

func TestKeeperHasNoSunsetSetter(t *testing.T) {
	typ := reflect.TypeOf(keeper.Keeper{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		switch name {
		case "InitGenesis", "ExportGenesis", "SunsetHeight", "GenesisCodeSet", "CheckMsg":
			continue
		default:
			require.NotContains(t, name, "Set")
			require.NotContains(t, name, "Update")
			require.NotContains(t, name, "Pause")
		}
	}
}

func TestUnrelatedMessagePasses(t *testing.T) {
	decorator, ctx, _ := newSunset(t)
	called := false
	_, err := decorator.AnteHandle(ctx.WithBlockHeight(1), txOf{msgs: []sdk.Msg{protoMsg{}}}, false, func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
		called = true
		return ctx, nil
	})
	require.NoError(t, err)
	require.True(t, called)
}
