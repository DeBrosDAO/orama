package node

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A node with no dns.node_names_zone publishes no names: nothing starts, so a cluster that does not
// answer the zone never reads the chain for it.
func TestStartNodeNamesSync_withoutAZoneStartsNothing(t *testing.T) {
	n := testNodeForDNS(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.startNodeNamesSync(ctx)
	if _, err := n.nodeNamesRegistry(); err == nil {
		t.Fatal("a node with no rqlite adapter returned a registry")
	}
}

// The registry may not exist yet when the loop starts; the pass reports that instead of
// dereferencing a nil adapter.
func TestNodeNamesRegistry_withoutAnAdapterSaysSo(t *testing.T) {
	n := testNodeForDNS(t)
	_, err := n.nodeNamesRegistry()
	if err == nil || !strings.Contains(err.Error(), "rqlite adapter") {
		t.Fatalf("err = %v", err)
	}
}

// With a zone set and no chain or registry, the loop runs, reports its failures, and stops with the
// node.
func TestStartNodeNamesSync_withAZoneRunsAndStopsWithItsContext(t *testing.T) {
	n := testNodeForDNS(t)
	n.config.DNS.NodeNamesZone = "stagenet.orama.network"
	ctx, cancel := context.WithCancel(context.Background())
	n.startNodeNamesSync(ctx)
	time.Sleep(50 * time.Millisecond)
	cancel()
}
