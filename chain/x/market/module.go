// Package market wires x/market's AppModule: listings, bids and royalty settlement.
package market

import (
	"encoding/json"
	"fmt"

	"cosmossdk.io/core/appmodule"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/DeBrosOfficial/network/chain/x/market/client/cli"
	"github.com/DeBrosOfficial/network/chain/x/market/keeper"
	"github.com/DeBrosOfficial/network/chain/x/market/types"
)

// ConsensusVersion is x/market's initial consensus version.
const ConsensusVersion = 1

var (
	_ module.AppModuleBasic = AppModuleBasic{}
	_ module.HasGenesis     = AppModule{}
	_ module.HasName        = AppModule{}
	_ module.HasServices    = AppModule{}

	_ appmodule.AppModule = AppModule{}
)

// AppModuleBasic defines the basic application module used by x/market.
type AppModuleBasic struct{}

// Name returns the module's name.
func (AppModuleBasic) Name() string { return types.ModuleName }

// RegisterLegacyAminoCodec registers x/market's messages on the legacy Amino codec.
func (AppModuleBasic) RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	types.RegisterLegacyAminoCodec(cdc)
}

// RegisterInterfaces registers x/market's sdk.Msg implementations.
func (AppModuleBasic) RegisterInterfaces(r cdctypes.InterfaceRegistry) {
	types.RegisterInterfaces(r)
}

// RegisterGRPCGatewayRoutes is a no-op: queries are served over gRPC.
func (AppModuleBasic) RegisterGRPCGatewayRoutes(client.Context, *gwruntime.ServeMux) {}

// DefaultGenesis returns x/market's default genesis state as raw JSON.
func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return cdc.MustMarshalJSON(types.DefaultGenesisState())
}

// ValidateGenesis performs genesis state validation for x/market.
func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var genState types.GenesisState
	if err := cdc.UnmarshalJSON(bz, &genState); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	return genState.Validate()
}

// AppModule implements x/market's application module.
type AppModule struct {
	AppModuleBasic
	keeper keeper.Keeper
}

// NewAppModule creates a new x/market AppModule.
func NewAppModule(k keeper.Keeper) AppModule {
	return AppModule{AppModuleBasic: AppModuleBasic{}, keeper: k}
}

// IsOnePerModuleType implements depinject.OnePerModuleType.
func (AppModule) IsOnePerModuleType() {}

// IsAppModule implements appmodule.AppModule.
func (AppModule) IsAppModule() {}

// RegisterServices registers x/market's Msg and Query servers.
func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterMsgServer(cfg.MsgServer(), keeper.NewMsgServer(am.keeper))
	types.RegisterQueryServer(cfg.QueryServer(), keeper.NewQueryServerImpl(am.keeper))
}

// InitGenesis performs genesis initialization for x/market.
func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) {
	var genState types.GenesisState
	cdc.MustUnmarshalJSON(data, &genState)
	if err := am.keeper.InitGenesis(ctx, genState); err != nil {
		panic(fmt.Errorf("failed to init %s genesis: %w", types.ModuleName, err))
	}
}

// ExportGenesis returns x/market's exported genesis state as raw JSON.
func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genState, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to export %s genesis: %w", types.ModuleName, err))
	}
	return cdc.MustMarshalJSON(genState)
}

// ConsensusVersion implements AppModule/ConsensusVersion.
func (AppModule) ConsensusVersion() uint64 { return ConsensusVersion }

// GetQueryCmd returns x/market's CLI query commands.
func (AppModule) GetQueryCmd() *cobra.Command {
	return cli.GetQueryCmd()
}
