// Package power wires x/power's AppModule: the bootstrap committee, the hand-over factor lambda,
// the capped stake share and the CometBFT validator updates this app returns instead of
// x/staking's own (plans/open-network/track-c-chain.md C4).
package power

import (
	"context"
	"encoding/json"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/DeBrosOfficial/network/chain/x/power/client/cli"
	"github.com/DeBrosOfficial/network/chain/x/power/keeper"
	"github.com/DeBrosOfficial/network/chain/x/power/types"
)

// ConsensusVersion is x/power's initial consensus version.
const ConsensusVersion = 1

var (
	_ module.AppModuleBasic  = AppModuleBasic{}
	_ module.HasABCIGenesis  = AppModule{}
	_ module.HasABCIEndBlock = AppModule{}
	_ module.HasName         = AppModule{}
	_ module.HasServices     = AppModule{}
)

// AppModuleBasic defines the basic application module used by x/power. There is no Msg service
// and nothing to register on the legacy Amino codec or the interface registry: x/power's genesis
// bootstrap committee carries only raw ed25519 pubkey bytes, never an Any (see
// types.BootstrapMember).
type AppModuleBasic struct{}

// Name returns the module's name.
func (AppModuleBasic) Name() string { return types.ModuleName }

// RegisterLegacyAminoCodec is a no-op: x/power has no Msg types.
func (AppModuleBasic) RegisterLegacyAminoCodec(*codec.LegacyAmino) {}

// RegisterInterfaces is a no-op: x/power has no Any-packed types.
func (AppModuleBasic) RegisterInterfaces(cdctypes.InterfaceRegistry) {}

// RegisterGRPCGatewayRoutes is a no-op: x/power does not generate a gRPC-gateway (REST) handler.
func (AppModuleBasic) RegisterGRPCGatewayRoutes(client.Context, *gwruntime.ServeMux) {}

// DefaultGenesis returns x/power's default genesis state as raw JSON.
func (b AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return cdc.MustMarshalJSON(types.DefaultGenesisState())
}

// ValidateGenesis performs genesis state validation for x/power.
func (b AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var genState types.GenesisState
	if err := cdc.UnmarshalJSON(bz, &genState); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	return genState.Validate()
}

// AppModule implements x/power's application module.
type AppModule struct {
	AppModuleBasic

	keeper         keeper.Keeper
	emissionKeeper types.EmissionKeeper
}

// NewAppModule creates a new x/power AppModule. emissionKeeper is used only to read x/emission's
// current epoch number (see types.EmissionKeeper's doc comment) - x/power never mints or burns.
func NewAppModule(k keeper.Keeper, emissionKeeper types.EmissionKeeper) AppModule {
	return AppModule{
		AppModuleBasic: AppModuleBasic{},
		keeper:         k,
		emissionKeeper: emissionKeeper,
	}
}

// IsOnePerModuleType implements the depinject.OnePerModuleType interface.
func (AppModule) IsOnePerModuleType() {}

// IsAppModule implements the appmodule.AppModule interface.
func (AppModule) IsAppModule() {}

// RegisterServices registers x/power's gRPC query service. There is no Msg service.
func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterQueryServer(cfg.QueryServer(), keeper.NewQueryServerImpl(am.keeper))
}

// InitGenesis performs genesis initialization for x/power and returns the genesis CometBFT
// validator set (the bootstrap committee) - see keeper.Keeper.InitGenesis.
func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) []abci.ValidatorUpdate {
	var genState types.GenesisState
	cdc.MustUnmarshalJSON(data, &genState)
	updates, err := am.keeper.InitGenesis(ctx, genState, am.emissionKeeper)
	if err != nil {
		panic(fmt.Errorf("failed to init %s genesis: %w", types.ModuleName, err))
	}
	return updates
}

// ExportGenesis returns x/power's exported genesis state as raw JSON.
func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genState, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to export %s genesis: %w", types.ModuleName, err))
	}
	return cdc.MustMarshalJSON(genState)
}

// ConsensusVersion implements AppModule/ConsensusVersion.
func (AppModule) ConsensusVersion() uint64 { return ConsensusVersion }

// EndBlock runs x/power's end-blocker and returns the CometBFT ValidatorUpdates for this block.
func (am AppModule) EndBlock(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	return EndBlocker(ctx, am.keeper, am.emissionKeeper)
}

// GetQueryCmd returns x/power's CLI query commands.
func (AppModule) GetQueryCmd() *cobra.Command {
	return cli.GetQueryCmd()
}
