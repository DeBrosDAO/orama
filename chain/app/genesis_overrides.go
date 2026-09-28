package app

import (
	"encoding/json"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/x/bank"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/distribution"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	"github.com/cosmos/cosmos-sdk/x/slashing"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"

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

// slashingGenesisOverride wraps x/slashing's own AppModuleBasic to default its slash fractions and
// signed-blocks window to plans/open-network/track-c-chain.md C4's spec (security review B3: "set
// the slashing genesis params per spec: downtime 0.01%, double-sign 5%, and a sensible
// signed-blocks window"). Stock x/slashing's own default SlashFractionDowntime (1%) is 100x the
// spec's 0.01%; SlashFractionDoubleSign (5%) already matches the spec and is left as the stock
// default. SignedBlocksWindow is widened from the stock default (100 blocks - too tight a window to
// tell a brief network blip from real downtime on anything but a toy chain) to 10,000, with
// MinSignedPerWindow and DowntimeJailDuration left at their stock defaults.
type slashingGenesisOverride struct {
	slashing.AppModuleBasic
}

// DefaultGenesis returns x/slashing's default genesis with the spec's slash fractions and a wider
// signed-blocks window.
func (slashingGenesisOverride) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	genState := slashingtypes.DefaultGenesisState()
	genState.Params.SlashFractionDowntime = math.LegacyNewDecWithPrec(1, 4) // 0.01%
	genState.Params.SignedBlocksWindow = 10_000
	return cdc.MustMarshalJSON(genState)
}
