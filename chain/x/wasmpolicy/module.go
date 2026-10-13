// Package wasmpolicy is the Orama wasm policy: upload sunset, the norama send rule's
// genesis parameters, and the state-deposit meter. It is not wasmd.
package wasmpolicy

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

	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/keeper"
	"github.com/DeBrosOfficial/network/chain/x/wasmpolicy/types"
)

// ConsensusVersion is wasmpolicy's initial consensus version.
const ConsensusVersion = 1

// ModuleName is the module's name in the app genesis map.
const ModuleName = types.ModuleName

var (
	_ module.AppModuleBasic = AppModuleBasic{}
	_ module.HasGenesis     = AppModule{}
	_ module.HasName        = AppModule{}
	_ appmodule.AppModule   = AppModule{}
)

// AppModuleBasic implements the stateless half of wasmpolicy.
type AppModuleBasic struct{}

// Name returns the module name.
func (AppModuleBasic) Name() string { return types.ModuleName }

// RegisterLegacyAminoCodec is a no-op: wasmpolicy has no Msg types.
func (AppModuleBasic) RegisterLegacyAminoCodec(*codec.LegacyAmino) {}

// RegisterInterfaces is a no-op: wasmpolicy has no Any-packed types.
func (AppModuleBasic) RegisterInterfaces(cdctypes.InterfaceRegistry) {}

// RegisterGRPCGatewayRoutes is a no-op: wasmpolicy has no gRPC gateway.
func (AppModuleBasic) RegisterGRPCGatewayRoutes(client.Context, *gwruntime.ServeMux) {}

// DefaultGenesis returns the default upload sunset and an empty code set.
// The bytes are encoding/json, not proto JSON: the state is not an Any.
func (AppModuleBasic) DefaultGenesis(codec.JSONCodec) json.RawMessage {
	bz, err := json.Marshal(types.DefaultGenesisState())
	if err != nil {
		panic(err)
	}
	return bz
}

// ValidateGenesis checks upload_sunset_height's code set.
func (AppModuleBasic) ValidateGenesis(_ codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var gs types.GenesisState
	if err := json.Unmarshal(bz, &gs); err != nil {
		return fmt.Errorf("unmarshal %s genesis: %w", types.ModuleName, err)
	}
	return gs.Validate()
}

// GetTxCmd returns nil: no message can change the sunset height.
func (AppModuleBasic) GetTxCmd() *cobra.Command { return nil }

// GetQueryCmd returns nil: queries are not part of C9's policy surface.
func (AppModuleBasic) GetQueryCmd() *cobra.Command { return nil }

// AppModule is the stateful wasmpolicy module. It registers no Msg service.
type AppModule struct {
	AppModuleBasic
	keeper keeper.Keeper
}

// NewAppModule returns the wasmpolicy module.
func NewAppModule(k keeper.Keeper) AppModule {
	return AppModule{keeper: k}
}

// IsOnePerModuleType implements depinject.OnePerModuleType.
func (AppModule) IsOnePerModuleType() {}

// IsAppModule implements appmodule.AppModule.
func (AppModule) IsAppModule() {}

// ConsensusVersion implements AppModule/ConsensusVersion.
func (AppModule) ConsensusVersion() uint64 { return ConsensusVersion }

// InitGenesis writes the sunset height once.
func (am AppModule) InitGenesis(ctx sdk.Context, _ codec.JSONCodec, bz json.RawMessage) {
	var gs types.GenesisState
	if err := json.Unmarshal(bz, &gs); err != nil {
		panic(fmt.Errorf("unmarshal %s genesis: %w", types.ModuleName, err))
	}
	if err := am.keeper.InitGenesis(ctx, gs); err != nil {
		panic(fmt.Errorf("init %s genesis: %w", types.ModuleName, err))
	}
}

// ExportGenesis returns the stored sunset height and genesis code set.
func (am AppModule) ExportGenesis(ctx sdk.Context, _ codec.JSONCodec) json.RawMessage {
	gs, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(fmt.Errorf("export %s genesis: %w", types.ModuleName, err))
	}
	bz, err := json.Marshal(gs)
	if err != nil {
		panic(err)
	}
	return bz
}
