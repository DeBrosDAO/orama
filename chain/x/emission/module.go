package emission

import (
	"context"
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

	"github.com/DeBrosOfficial/network/chain/x/emission/client/cli"
	"github.com/DeBrosOfficial/network/chain/x/emission/keeper"
	"github.com/DeBrosOfficial/network/chain/x/emission/types"
)

// ConsensusVersion is x/emission's initial consensus version.
const ConsensusVersion = 1

var (
	_ module.AppModuleBasic = AppModuleBasic{}
	_ module.HasGenesis     = AppModule{}
	_ module.HasName        = AppModule{}
	_ module.HasServices    = AppModule{}

	_ appmodule.AppModule       = AppModule{}
	_ appmodule.HasBeginBlocker = AppModule{}
	_ appmodule.HasEndBlocker   = AppModule{}
)

// AppModuleBasic defines the basic application module used by x/emission. There is no Msg
// service and nothing to register on the legacy Amino codec or the interface registry: x/emission
// carries no messages and no Any-packed types.
type AppModuleBasic struct{}

// Name returns the module's name.
func (AppModuleBasic) Name() string { return types.ModuleName }

// RegisterLegacyAminoCodec is a no-op: x/emission has no Msg types.
func (AppModuleBasic) RegisterLegacyAminoCodec(*codec.LegacyAmino) {}

// RegisterInterfaces is a no-op: x/emission has no Any-packed types.
func (AppModuleBasic) RegisterInterfaces(cdctypes.InterfaceRegistry) {}

// RegisterGRPCGatewayRoutes is a no-op: x/emission does not generate a gRPC-gateway (REST)
// handler. Queries are available over gRPC and the `oramad query emission` CLI.
func (AppModuleBasic) RegisterGRPCGatewayRoutes(client.Context, *gwruntime.ServeMux) {}

// DefaultGenesis returns x/emission's default genesis state as raw JSON.
func (b AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return cdc.MustMarshalJSON(types.DefaultGenesisState())
}

// ValidateGenesis performs genesis state validation for x/emission.
func (b AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var genState types.GenesisState
	if err := cdc.UnmarshalJSON(bz, &genState); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	return genState.Validate()
}

// AppModule implements x/emission's application module.
type AppModule struct {
	AppModuleBasic

	keeper keeper.Keeper
}

// NewAppModule creates a new x/emission AppModule.
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

// RegisterServices registers x/emission's gRPC query service. There is no Msg service.
func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterQueryServer(cfg.QueryServer(), keeper.NewQueryServerImpl(am.keeper))
}

// InitGenesis performs genesis initialization for x/emission. It returns no validator updates.
func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) {
	var genState types.GenesisState
	cdc.MustUnmarshalJSON(data, &genState)
	if err := am.keeper.InitGenesis(ctx, genState); err != nil {
		panic(fmt.Errorf("failed to init %s genesis: %w", types.ModuleName, err))
	}
}

// ExportGenesis returns x/emission's exported genesis state as raw JSON.
func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genState, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to export %s genesis: %w", types.ModuleName, err))
	}
	return cdc.MustMarshalJSON(genState)
}

// ConsensusVersion implements AppModule/ConsensusVersion.
func (AppModule) ConsensusVersion() uint64 { return ConsensusVersion }

// BeginBlock runs x/emission's begin-blocker.
func (am AppModule) BeginBlock(ctx context.Context) error {
	return BeginBlocker(ctx, am.keeper)
}

// EndBlock runs x/emission's end-blocker.
func (am AppModule) EndBlock(ctx context.Context) error {
	return EndBlocker(ctx, am.keeper)
}

// GetQueryCmd returns x/emission's CLI query commands.
func (AppModule) GetQueryCmd() *cobra.Command {
	return cli.GetQueryCmd()
}
