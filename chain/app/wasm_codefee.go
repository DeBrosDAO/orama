//go:build cgo && !nowasm

package app

import (
	"context"

	"github.com/cosmos/cosmos-sdk/types/module"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/cosmos/gogoproto/grpc"
	googlegrpc "google.golang.org/grpc"

	"github.com/CosmWasm/wasmd/x/wasm"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"

	policykeeper "github.com/DeBrosOfficial/network/chain/x/wasmpolicy/keeper"
)

// codeFeeModule is wasmd's module with its Msg service wrapped so every message that stores code
// first pays the per-byte code fee (bugboard 3436): the uncompressed wasm size times deposit_per_byte,
// burned from the payer's bank balance. It runs inside the message, not in the ante chain, so a
// store that wasmd then rejects reverts with the fee, and a payer who cannot pay stores nothing.
// Genesis-embedded contracts are written by InitGenesis and never reach it.
type codeFeeModule struct {
	wasm.AppModule
	fees       policykeeper.Keeper
	isContract func(ctx context.Context, addr sdk.AccAddress) bool
}

// RegisterServices registers wasmd's services with the Msg service wrapped.
func (m codeFeeModule) RegisterServices(cfg module.Configurator) {
	m.AppModule.RegisterServices(codeFeeConfigurator{Configurator: cfg, wrap: m.wrap})
}

func (m codeFeeModule) wrap(srv wasmtypes.MsgServer) wasmtypes.MsgServer {
	return codeFeeMsgServer{MsgServer: srv, fees: m.fees, isContract: m.isContract}
}

// codeFeeConfigurator hands wasmd's module a Msg registrar that wraps the wasm Msg service.
type codeFeeConfigurator struct {
	module.Configurator
	wrap func(wasmtypes.MsgServer) wasmtypes.MsgServer
}

func (c codeFeeConfigurator) MsgServer() grpc.Server {
	return codeFeeRegistrar{Server: c.Configurator.MsgServer(), wrap: c.wrap}
}

type codeFeeRegistrar struct {
	grpc.Server
	wrap func(wasmtypes.MsgServer) wasmtypes.MsgServer
}

func (r codeFeeRegistrar) RegisterService(sd *googlegrpc.ServiceDesc, handler interface{}) {
	if srv, ok := handler.(wasmtypes.MsgServer); ok {
		handler = r.wrap(srv)
	}
	r.Server.RegisterService(sd, handler)
}

// codeFeeMsgServer charges the code fee before each of the three wasmd messages that store code.
type codeFeeMsgServer struct {
	wasmtypes.MsgServer
	fees       policykeeper.Keeper
	isContract func(ctx context.Context, addr sdk.AccAddress) bool
}

func (s codeFeeMsgServer) charge(ctx context.Context, sender string, wasmCode []byte) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return s.fees.ChargeCodeFee(sdkCtx, depositPayer(sdkCtx, sender, s.isContract), wasmCode)
}

func (s codeFeeMsgServer) StoreCode(ctx context.Context, msg *wasmtypes.MsgStoreCode) (*wasmtypes.MsgStoreCodeResponse, error) {
	if err := s.charge(ctx, msg.Sender, msg.WASMByteCode); err != nil {
		return nil, err
	}
	return s.MsgServer.StoreCode(ctx, msg)
}

func (s codeFeeMsgServer) StoreAndInstantiateContract(ctx context.Context, msg *wasmtypes.MsgStoreAndInstantiateContract) (*wasmtypes.MsgStoreAndInstantiateContractResponse, error) {
	if err := s.charge(ctx, msg.Authority, msg.WASMByteCode); err != nil {
		return nil, err
	}
	return s.MsgServer.StoreAndInstantiateContract(ctx, msg)
}

func (s codeFeeMsgServer) StoreAndMigrateContract(ctx context.Context, msg *wasmtypes.MsgStoreAndMigrateContract) (*wasmtypes.MsgStoreAndMigrateContractResponse, error) {
	if err := s.charge(ctx, msg.Authority, msg.WASMByteCode); err != nil {
		return nil, err
	}
	return s.MsgServer.StoreAndMigrateContract(ctx, msg)
}
