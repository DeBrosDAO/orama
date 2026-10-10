package node

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/nodenames"
)

// A node with no dns.node_names_zone publishes no names: there is no sync, so a cluster that does
// not answer the zone never reads the chain for it.
func TestNodeNamesSyncer_withoutAZoneIsNil(t *testing.T) {
	n := testNodeForDNS(t)
	if s := n.nodeNamesSyncer(); s != nil {
		t.Fatalf("a node with no zone has a sync: %+v", s)
	}
}

func TestNodeNamesSyncer_withAZoneReadsTheChainAndTheRegistry(t *testing.T) {
	n := testNodeForDNS(t)
	n.config.DNS.NodeNamesZone = "nodes.stagenet.orama.network"
	s := n.nodeNamesSyncer()
	if s == nil || s.Zone != "nodes.stagenet.orama.network" {
		t.Fatalf("sync = %+v", s)
	}
	if _, ok := s.Chain.(nodenames.ReaderChain); !ok {
		t.Fatalf("the chain is %T, want the node's RPC reader", s.Chain)
	}
	// With no rqlite adapter yet the pass says so instead of dereferencing nil.
	if _, err := s.Registry(); err == nil || !strings.Contains(err.Error(), "rqlite adapter") {
		t.Fatalf("registry err = %v", err)
	}
}

func TestRefusedKey_differsForADifferentSetOfTheSameSize(t *testing.T) {
	a := []nodenames.Refusal{{Name: "alice", Reason: "x"}}
	b := []nodenames.Refusal{{Name: "bob", Reason: "x"}}
	if refusedKey(a) == refusedKey(b) {
		t.Fatal("two different refusals have one key")
	}
	if refusedKey(nil) != "" {
		t.Fatal("no refusals must have the empty key")
	}
	two := []nodenames.Refusal{{Name: "b", Reason: "x"}, {Name: "a", Reason: "x"}}
	rev := []nodenames.Refusal{two[1], two[0]}
	if refusedKey(two) != refusedKey(rev) {
		t.Fatal("the order of the refusals changed the key")
	}
}
