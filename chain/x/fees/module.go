// Package fees wires x/fees's AppModule: the EIP-1559-style base fee, earnings accounts and the
// state-deposit ledger (plans/open-network/track-c-chain.md C2).
package fees

import (
	"context"
	"encoding/json"
	"fmt"

	gwruntime "github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/spf13/cobra"

	"cosmossdk.io/core/appmodule"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/DeBrosOfficial/network/chain/x/fees/client/cli"
	"github.com/DeBrosOfficial/network/chain/x/fees/keeper"
	"github.com/DeBrosOfficial/network/chain/x/fees/types"
)

// ConsensusVersion is x/fees's initial consensus version.
const ConsensusVersion = 1

var (
	_ module.AppModuleBasic = AppModuleBasic{}
	_ module.HasGenesis     = AppModule{}
	_ module.HasName        = AppModule{}
	_ module.HasServices    = AppModule{}

	_ appmodule.AppModule     = AppModule{}
	_ appmodule.HasEndBlocker = AppModule{}
)

// AppModuleBasic defines the basic application module used by x/fees. There is no Msg service and
// nothing to register on the legacy Amino codec or the interface registry: fees are paid through
// the ante handler, and earnings/deposits are moved only by other modules' keeper calls.
type AppModuleBasic struct{}

// Name returns the module's name.
func (AppModuleBasic) Name() string { return types.ModuleName }

// RegisterLegacyAminoCodec is a no-op: x/fees has no Msg types.
func (AppModuleBasic) RegisterLegacyAminoCodec(*codec.LegacyAmino) {}

// RegisterInterfaces is a no-op: x/fees has no Any-packed types.
func (AppModuleBasic) RegisterInterfaces(cdctypes.InterfaceRegistry) {}

// RegisterGRPCGatewayRoutes is a no-op: x/fees does not generate a gRPC-gateway (REST) handler.
func (AppModuleBasic) RegisterGRPCGatewayRoutes(client.Context, *gwruntime.ServeMux) {}

// DefaultGenesis returns x/fees's default genesis state as raw JSON.
func (b AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return cdc.MustMarshalJSON(types.DefaultGenesisState())
}

// ValidateGenesis performs genesis state validation for x/fees.
func (b AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var genState types.GenesisState
	if err := cdc.UnmarshalJSON(bz, &genState); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	return genState.Validate()
}

// AppModule implements x/fees's application module.
type AppModule struct {
	AppModuleBasic

	keeper keeper.Keeper
}

// NewAppModule creates a new x/fees AppModule.
func NewAppModule(k keeper.Keeper) AppModule {
	return AppModule{
		AppModuleBasic: AppModuleBasic{},
		keeper:         k,
	}
}

// IsOnePerModuleType implements the depinject.OnePerModuleType interface.
func (AppModule) IsOnePerModuleType() {}

// IsAppModule implements the appmodule.AppModule interface.
func (AppModule) IsAppModule() {}

// RegisterServices registers x/fees's gRPC query service. There is no Msg service.
func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterQueryServer(cfg.QueryServer(), keeper.NewQueryServerImpl(am.keeper))
}

// InitGenesis performs genesis initialization for x/fees.
func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) {
	var genState types.GenesisState
	cdc.MustUnmarshalJSON(data, &genState)
	if err := am.keeper.InitGenesis(ctx, genState); err != nil {
		panic(fmt.Errorf("failed to init %s genesis: %w", types.ModuleName, err))
	}
}

// ExportGenesis returns x/fees's exported genesis state as raw JSON.
func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genState, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to export %s genesis: %w", types.ModuleName, err))
	}
	return cdc.MustMarshalJSON(genState)
}

// ConsensusVersion implements AppModule/ConsensusVersion.
func (AppModule) ConsensusVersion() uint64 { return ConsensusVersion }

// EndBlock advances the base fee for the next block.
func (am AppModule) EndBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return am.keeper.AdvanceBaseFee(sdkCtx)
}

// GetQueryCmd returns x/fees's CLI query commands.
func (AppModule) GetQueryCmd() *cobra.Command {
	return cli.GetQueryCmd()
}
