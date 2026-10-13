//go:build cgo && !nowasm

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"

	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	policykeeper "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/keeper"
	policytypes "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// stubFees records what the code fee burns; every other FeesKeeper method is unused by it.
type stubFees struct {
	policykeeper.FeesKeeper
	burned []math.Int
	refuse bool
}

func (s *stubFees) BurnFromBalance(_ context.Context, _ sdk.AccAddress, amount math.Int, _ string) error {
	if s.refuse {
		return errors.New("insufficient funds")
	}
	s.burned = append(s.burned, amount)
	return nil
}

// stubWasm records which store messages reached wasmd.
type stubWasm struct {
	wasmtypes.MsgServer
	reached []string
}

func (s *stubWasm) StoreCode(context.Context, *wasmtypes.MsgStoreCode) (*wasmtypes.MsgStoreCodeResponse, error) {
	s.reached = append(s.reached, "store")
	return &wasmtypes.MsgStoreCodeResponse{}, nil
}

func (s *stubWasm) StoreAndInstantiateContract(context.Context, *wasmtypes.MsgStoreAndInstantiateContract) (*wasmtypes.MsgStoreAndInstantiateContractResponse, error) {
	s.reached = append(s.reached, "instantiate")
	return &wasmtypes.MsgStoreAndInstantiateContractResponse{}, nil
}

func (s *stubWasm) StoreAndMigrateContract(context.Context, *wasmtypes.MsgStoreAndMigrateContract) (*wasmtypes.MsgStoreAndMigrateContractResponse, error) {
	s.reached = append(s.reached, "migrate")
	return &wasmtypes.MsgStoreAndMigrateContractResponse{}, nil
}

func codeFeeFixture(t *testing.T) (sdk.Context, *stubFees, *stubWasm, codeFeeMsgServer) {
	t.Helper()
	key := storetypes.NewKVStoreKey(policytypes.StoreKey)
	ctx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("t_codefee")).Ctx
	fees := &stubFees{}
	k := policykeeper.NewKeeper(runtime.NewKVStoreService(key), fees)
	require.NoError(t, k.InitGenesis(ctx, policytypes.DefaultGenesisState()))
	inner := &stubWasm{}
	return ctx, fees, inner, codeFeeMsgServer{MsgServer: inner, fees: k, isContract: func(context.Context, sdk.AccAddress) bool { return false }}
}

func TestCodeFeeMsgServer_everyStoringMessageIsChargedBeforeWasmdRunsIt(t *testing.T) {
	ctx, fees, inner, srv := codeFeeFixture(t)
	owner := sdk.AccAddress("code_fee_owner_______").String()
	wasm := make([]byte, 100)
	want := math.NewInt(100).MulRaw(policytypes.DefaultDepositPerByte)

	_, err := srv.StoreCode(ctx, &wasmtypes.MsgStoreCode{Sender: owner, WASMByteCode: wasm})
	require.NoError(t, err)
	_, err = srv.StoreAndInstantiateContract(ctx, &wasmtypes.MsgStoreAndInstantiateContract{Authority: owner, WASMByteCode: wasm})
	require.NoError(t, err)
	_, err = srv.StoreAndMigrateContract(ctx, &wasmtypes.MsgStoreAndMigrateContract{Authority: owner, WASMByteCode: wasm})
	require.NoError(t, err)

	require.Equal(t, []string{"store", "instantiate", "migrate"}, inner.reached)
	require.Len(t, fees.burned, 3)
	for _, got := range fees.burned {
		require.True(t, got.Equal(want))
	}
}

func TestCodeFeeMsgServer_aRefusedFeeNeverReachesWasmd(t *testing.T) {
	ctx, fees, inner, srv := codeFeeFixture(t)
	fees.refuse = true
	owner := sdk.AccAddress("code_fee_owner_______").String()
	wasm := make([]byte, 100)

	_, err := srv.StoreCode(ctx, &wasmtypes.MsgStoreCode{Sender: owner, WASMByteCode: wasm})
	require.ErrorIs(t, err, policytypes.ErrCodeFee)
	_, err = srv.StoreAndInstantiateContract(ctx, &wasmtypes.MsgStoreAndInstantiateContract{Authority: owner, WASMByteCode: wasm})
	require.ErrorIs(t, err, policytypes.ErrCodeFee)
	_, err = srv.StoreAndMigrateContract(ctx, &wasmtypes.MsgStoreAndMigrateContract{Authority: owner, WASMByteCode: wasm})
	require.ErrorIs(t, err, policytypes.ErrCodeFee)
	require.Empty(t, inner.reached)
}
