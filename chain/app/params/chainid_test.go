package params_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DeBrosOfficial/network/chain/app/params"
)

func TestClassOfChainID(t *testing.T) {
	cases := map[string]params.ChainClass{
		// The ids the repository really uses.
		"orama-stagenet-10":                params.ChainStagenet,
		"orama-stagenet-1":                 params.ChainStagenet,
		"orama-devnet-9":                   params.ChainDevnet,
		"orama-devnet-e2e-run7":            params.ChainDevnet,
		"orama-localnet-1":                 params.ChainLocalnet,
		"orama-localnet-bench-1":           params.ChainLocalnet,
		"orama-localnet-keepertest":        params.ChainLocalnet,
		"orama-localnet-shielded-wallet-1": params.ChainLocalnet,
		// A test network of one's own: one name word that starts with a letter.
		"orama-pubnet-stagenet-1": params.ChainStagenet,
		"orama-x-devnet-1":        params.ChainDevnet,
		"orama-x-localnet-1":      params.ChainLocalnet,
		"orama-1x-stagenet-1":     params.ChainProduction,
		// A class word is never the name word: these are ambiguous, so they are production.
		"orama-devnet-localnet-1":   params.ChainProduction,
		"orama-localnet-devnet-1":   params.ChainProduction,
		"orama-stagenet-stagenet-1": params.ChainProduction,
		"orama-devnet-devnet-1":     params.ChainProduction,
		"orama-pubnet-localnet-1":   params.ChainLocalnet,
		"orama-localnet-devnet2-1":  params.ChainLocalnet,
		"orama-devnet-localnetx-1":  params.ChainDevnet,
		"orama-a-b-stagenet-1":      params.ChainProduction,
		// Production, and ids that only look like a test chain.
		"orama-1":               params.ChainProduction,
		"orama-mainnet-1":       params.ChainProduction,
		"orama-testnet-1":       params.ChainProduction,
		"orama-1-stagenet-x":    params.ChainProduction,
		"orama-1-devnet-x":      params.ChainProduction,
		"orama-1-localnet-x":    params.ChainProduction,
		"mainnet-stagenet-1":    params.ChainProduction,
		"x-orama-stagenet-1":    params.ChainProduction,
		"orama-stagenet-1-":     params.ChainProduction,
		"orama-stagenet-":       params.ChainProduction,
		"orama-stagenet":        params.ChainProduction,
		"-stagenet-":            params.ChainProduction,
		"-localnet-":            params.ChainProduction,
		"orama-stagenet-1\n":    params.ChainProduction,
		"orama-stagenet-1 ":     params.ChainProduction,
		"orama-stagenet--1":     params.ChainProduction,
		"orama-Stagenet-1":      params.ChainProduction,
		"orama-stagenet-1/../x": params.ChainProduction,
		"":                      params.ChainProduction,
	}
	for id, want := range cases {
		require.Equal(t, want, params.ClassOfChainID(id), "chain id %q", id)
		require.Equal(t, want != params.ChainProduction, params.IsNonProductionChainID(id), "chain id %q", id)
	}
}
