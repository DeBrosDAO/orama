package app

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

func init() {
	// Default sdk.DefaultBondDenom to norama so a plain `oramad init` (no --default-denom flag)
	// still produces a genesis denominated in norama rather than the SDK's own "stake" default.
	// `oramad init --default-denom <x>` (genutilcli.InitCmd, x/genutil FlagDefaultBondDenom)
	// overrides this the same way it would override any other default.
	sdk.DefaultBondDenom = params.BaseDenom
}

// SetAddressPrefixes configures the SDK's global bech32 address config with Orama's prefixes
// (plans/open-network/track-c-chain.md C1: "The bech32 prefix is orama"). It must run exactly
// once, before the SDK config is sealed and before the app (or anything that parses or formats an
// address) is constructed.
func SetAddressPrefixes() {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	cfg.SetBech32PrefixForValidator(params.Bech32PrefixValAddr, params.Bech32PrefixValPub)
	cfg.SetBech32PrefixForConsensusNode(params.Bech32PrefixConsAddr, params.Bech32PrefixConsPub)
	cfg.SetCoinType(params.CoinType)
}
