// Package shielded wires x/shielded's AppModule: the Ironwood shielded pool, its turnstiles, the
// unshield cap and queue, and the nullifier set (plans/open-network/track-c-chain.md C12).
package shielded

import (
	"context"
	"encoding/json"
	"fmt"

	"cosmossdk.io/core/appmodule"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/runtime"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/DeBrosOfficial/network/chain/x/shielded/keeper"
	"github.com/DeBrosOfficial/network/chain/x/shielded/types"
)

// ConsensusVersion is x/shielded's initial consensus version.
const ConsensusVersion = 1

var (
	_ module.AppModuleBasic = AppModuleBasic{}
	_ module.HasGenesis     = AppModule{}
	_ module.HasName        = AppModule{}
	_ module.HasServices    = AppModule{}

	_ appmodule.AppModule     = AppModule{}
	_ appmodule.HasEndBlocker = AppModule{}
)

// AppModuleBasic defines the basic application module used by x/shielded.
type AppModuleBasic struct{}

// Name returns the module's name.
func (AppModuleBasic) Name() string { return types.ModuleName }

// RegisterLegacyAminoCodec registers x/shielded's amino types.
func (AppModuleBasic) RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	types.RegisterLegacyAminoCodec(cdc)
}

// RegisterInterfaces registers x/shielded's protobuf interfaces.
func (AppModuleBasic) RegisterInterfaces(reg cdctypes.InterfaceRegistry) {
	types.RegisterInterfaces(reg)
}

// RegisterGRPCGatewayRoutes is a no-op: x/shielded does not generate a gRPC-gateway handler.
func (AppModuleBasic) RegisterGRPCGatewayRoutes(client.Context, *gwruntime.ServeMux) {}

// DefaultGenesis returns x/shielded's default genesis state as raw JSON.
func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return cdc.MustMarshalJSON(types.DefaultGenesisState())
}

// ValidateGenesis performs genesis state validation for x/shielded.
func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var gs types.GenesisState
	if err := cdc.UnmarshalJSON(bz, &gs); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	return gs.Validate()
}

// AppModule implements x/shielded's application module.
type AppModule struct {
	AppModuleBasic

	keeper keeper.Keeper
}

// NewAppModule creates a new x/shielded AppModule.
func NewAppModule(k keeper.Keeper) AppModule {
	return AppModule{AppModuleBasic: AppModuleBasic{}, keeper: k}
}

// IsOnePerModuleType implements the depinject.OnePerModuleType interface.
func (AppModule) IsOnePerModuleType() {}

// IsAppModule implements the appmodule.AppModule interface.
func (AppModule) IsAppModule() {}

// RegisterServices registers x/shielded's Msg and query servers.
func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterMsgServer(cfg.MsgServer(), keeper.NewMsgServerImpl(am.keeper))
	types.RegisterQueryServer(cfg.QueryServer(), keeper.NewQueryServerImpl(am.keeper))
}

// InitGenesis performs genesis initialization for x/shielded.
func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) {
	var gs types.GenesisState
	cdc.MustUnmarshalJSON(data, &gs)
	if err := am.keeper.InitGenesis(ctx, gs); err != nil {
		panic(fmt.Errorf("failed to init %s genesis: %w", types.ModuleName, err))
	}
}

// ExportGenesis returns x/shielded's exported genesis state as raw JSON.
func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	gs, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to export %s genesis: %w", types.ModuleName, err))
	}
	return cdc.MustMarshalJSON(gs)
}

// ConsensusVersion implements AppModule/ConsensusVersion.
func (AppModule) ConsensusVersion() uint64 { return ConsensusVersion }

// EndBlock pays the unshield queue, flushes the block's nullifiers and records the anchor.
func (am AppModule) EndBlock(ctx context.Context) error {
	return am.keeper.EndBlock(sdk.UnwrapSDKContext(ctx))
}
