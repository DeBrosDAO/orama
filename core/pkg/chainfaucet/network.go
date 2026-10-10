package chainfaucet

import "strings"

// TestNetworkMarkers are the chain id fragments of the networks that may run a faucet. The chain
// refuses MsgFaucet on any other chain id (x/emission keeper nonProductionChainIDMarkers, which
// core cannot import), and every client of the faucet says so before it signs.
// TestIsTestNetwork_matchesTheChain fails when the chain's list and this one differ.
var TestNetworkMarkers = []string{"-stagenet-", "-devnet-", "-localnet-"}

// IsTestNetwork reports whether chainID is a test network's: the only chain ids a faucet signs for.
func IsTestNetwork(chainID string) bool {
	for _, marker := range TestNetworkMarkers {
		if strings.Contains(chainID, marker) {
			return true
		}
	}
	return false
}
