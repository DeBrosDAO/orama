package app

import (
	"encoding/json"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/x/bank"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/distribution"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// bankGenesisOverride wraps x/bank's own AppModuleBasic to register norama/ORAMA's
// DenomMetadata in the default genesis, which upstream x/bank leaves empty by default.
type bankGenesisOverride struct {
	bank.AppModuleBasic
}

// DefaultGenesis returns x/bank's default genesis with Orama's denom metadata added.
func (bankGenesisOverride) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	genState := banktypes.DefaultGenesisState()
	genState.DenomMetadata = []banktypes.Metadata{
		{
			Description: "The native token of the Orama Network",
			Base:        params.BaseDenom,
			Display:     params.DisplayDenom,
			Name:        "Orama",
			Symbol:      params.DisplayDenom,
			DenomUnits: []*banktypes.DenomUnit{
				{Denom: params.BaseDenom, Exponent: 0},
				{Denom: params.DisplayDenom, Exponent: params.DenomDecimals},
			},
		},
	}
	return cdc.MustMarshalJSON(genState)
}

// distrGenesisOverride wraps x/distribution's own AppModuleBasic to default community_tax to
// zero: plans/open-network.md D13/D2 has no community pool spend path on this chain (no x/gov,
// see app.UnreachableAuthority), so a nonzero tax would just accumulate in a pool nothing can ever
// spend from.
type distrGenesisOverride struct {
	distribution.AppModuleBasic
}

// DefaultGenesis returns x/distribution's default genesis with community_tax set to zero.
func (distrGenesisOverride) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	genState := distrtypes.DefaultGenesisState()
	genState.Params.CommunityTax = math.LegacyZeroDec()
	return cdc.MustMarshalJSON(genState)
}
