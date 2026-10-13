package main

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

// init applies the chain's bech32 prefixes to the SDK config, as oramad does at start-up, so an
// address this program parses (a message signer, a binding's signer) is read as an orama address.
// chain/app is not imported for it: that would link the node.
func init() {
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(params.Bech32Prefix, params.Bech32PrefixAccPub)
	cfg.SetBech32PrefixForValidator(params.Bech32PrefixValAddr, params.Bech32PrefixValPub)
	cfg.SetBech32PrefixForConsensusNode(params.Bech32PrefixConsAddr, params.Bech32PrefixConsPub)
	cfg.SetCoinType(params.CoinType)
}
