// Package netclass tells a production chain id from a test network's.
//
// The chain refuses, at its genesis, what only a test network may do: a
// bootstrap committee smaller than ProductionMinCommitteeSize, a faucet, a
// genesis supply and short epochs. It decides by the chain id alone (a test
// network's is exactly a localnet, devnet or stagenet id), and the CLI makes the same
// decision before it builds a genesis the chain would refuse, so the mistake is
// found before any machine is touched. TestNetclass_matchesChain keeps the
// constants equal to the chain's.
package netclass

import (
	"fmt"
	"regexp"
)

// MaxChainIDLen is the longest chain id any part of the system takes.
const MaxChainIDLen = 48

// chainIDPattern is a chain id: lowercase letters, digits and hyphens, starting and ending with
// a letter or digit. It is the one definition: the network manifest, the chain home an install
// creates, the transactions the client signs and the status a node reports are all held to it,
// so a chain id that is shown for approval or written to a file cannot carry a control
// character, and cannot differ in case from the one it was checked as.
var chainIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,` + fmt.Sprint(MaxChainIDLen-2) + `}[a-z0-9])?$`)

// ValidChainID reports whether id is a well-formed chain id: 1 to MaxChainIDLen characters of
// a-z, 0-9 and '-', not starting or ending with '-'.
func ValidChainID(id string) bool { return chainIDPattern.MatchString(id) }

// TestChainIDForm names, in messages, the chain ids of a test network.
const TestChainIDForm = "orama-[<name>-](localnet|devnet|stagenet)-<id>"

// testChainIDPattern is the chain's own rule for a test network's chain id, the same pattern as
// chain/app/params testChainIDPattern (TestNetclass_matchesChain keeps them equal): "orama-",
// optionally one name word that starts with a letter, the class, then one or more lowercase
// alphanumeric words joined by hyphens. It is anchored at both ends, so an id that only contains a
// class word ("orama-1-stagenet-x") is a production id. Group 2 is the name word and group 3 the class.
var testChainIDPattern = regexp.MustCompile(`^orama-(([a-z][a-z0-9]*)-)?(localnet|devnet|stagenet)-[a-z0-9]+(-[a-z0-9]+)*$`)

// classWords are the class names; none may be the optional name word (chain/app/params classWords,
// kept equal by TestNetclass_matchesChain).
var classWords = []string{"localnet", "devnet", "stagenet"}

// testClass returns the class chainID names, or "" when it is not exactly a test network's id (a
// name word that is a class word makes it not one).
func testClass(chainID string) string {
	m := testChainIDPattern.FindStringSubmatch(chainID)
	if m == nil {
		return ""
	}
	for _, w := range classWords {
		if m[2] == w {
			return ""
		}
	}
	return m[3]
}

// ProductionMinCommittee is the smallest bootstrap committee a production
// chain id may start with (chain/x/power/types ProductionMinCommitteeSize).
const ProductionMinCommittee = 30

// IsTestNetwork reports whether chainID names a network that is not production: the only chain ids
// a faucet runs on.
func IsTestNetwork(chainID string) bool { return !IsProduction(chainID) }

// IsLocalnet reports whether chainID names a localnet, the class on which the chain locks no parameter.
func IsLocalnet(chainID string) bool { return testClass(chainID) == "localnet" }

// IsDevnet reports whether chainID names a devnet, the chain a script builds for one e2e run.
func IsDevnet(chainID string) bool { return testClass(chainID) == "devnet" }

// IsProduction reports whether chainID names a production network: one that is not exactly a
// localnet, devnet or stagenet id.
func IsProduction(chainID string) bool { return testClass(chainID) == "" }

// CheckCommittee refuses a production chain id whose bootstrap committee is
// smaller than the floor. A test network may have any size of at least one.
func CheckCommittee(chainID string, size int) error {
	if size < 1 {
		return fmt.Errorf("a bootstrap committee needs at least one member")
	}
	if IsProduction(chainID) && size < ProductionMinCommittee {
		return fmt.Errorf("chain id %q is not of the form %s, so it is a production network, and a production network starts with at least %d bootstrap validators, not %d: "+
			"use a chain id such as orama-<name>-stagenet-1 for a test network",
			chainID, TestChainIDForm, ProductionMinCommittee, size)
	}
	return nil
}
