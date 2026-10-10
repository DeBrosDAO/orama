// Package netclass tells a production chain id from a test network's.
//
// The chain refuses, at its genesis, what only a test network may do: a
// bootstrap committee smaller than ProductionMinCommitteeSize, a faucet, a
// genesis supply and short epochs. It decides by the chain id alone (a test
// network's carries one of NonProductionMarkers), and the CLI makes the same
// decision before it builds a genesis the chain would refuse, so the mistake is
// found before any machine is touched. TestNetclass_matchesChain keeps the
// constants equal to the chain's.
package netclass

import (
	"fmt"
	"strings"
)

// The chain id fragments of the networks that are not production.
const (
	MarkerStagenet = "-stagenet-"
	MarkerDevnet   = "-devnet-"
	MarkerLocalnet = "-localnet-"
)

// NonProductionMarkers are the fragments a test network's chain id carries:
// chain/x/power/keeper and chain/x/emission/keeper nonProductionChainIDMarkers.
var NonProductionMarkers = []string{MarkerStagenet, MarkerDevnet, MarkerLocalnet}

// ProductionMinCommittee is the smallest bootstrap committee a production
// chain id may start with (chain/x/power/types ProductionMinCommitteeSize).
const ProductionMinCommittee = 30

// IsProduction reports whether chainID names a production network: one that
// carries none of NonProductionMarkers.
func IsProduction(chainID string) bool {
	for _, m := range NonProductionMarkers {
		if strings.Contains(chainID, m) {
			return false
		}
	}
	return true
}

// CheckCommittee refuses a production chain id whose bootstrap committee is
// smaller than the floor. A test network may have any size of at least one.
func CheckCommittee(chainID string, size int) error {
	if size < 1 {
		return fmt.Errorf("a bootstrap committee needs at least one member")
	}
	if IsProduction(chainID) && size < ProductionMinCommittee {
		return fmt.Errorf("chain id %q has none of %s, so it is a production network, and a production network starts with at least %d bootstrap validators, not %d: "+
			"use a chain id such as orama-<name>-stagenet-1 for a test network",
			chainID, strings.Join(NonProductionMarkers, ", "), ProductionMinCommittee, size)
	}
	return nil
}
