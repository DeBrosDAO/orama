package chainfaucet

import "github.com/DeBrosOfficial/network/pkg/netclass"

// IsTestNetwork reports whether chainID is a test network's: the only chain ids a faucet signs for.
// The chain refuses MsgFaucet on any other chain id (x/emission keeper nonProductionChainIDMarkers,
// which core cannot import); netclass keeps the list and tests it against the chain's.
func IsTestNetwork(chainID string) bool { return netclass.IsTestNetwork(chainID) }
