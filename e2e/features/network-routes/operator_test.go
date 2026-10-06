//go:build e2e_fleet

package networkroutes

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// TestNetworkRoutes_asOperator runs every test that needs an operator
// against ONE shared fixture (operatorOwner): the cluster's operator list is
// changed once, by this test, and restored when every parallel subtest has
// finished, instead of once per test from tests running side by side.
func TestNetworkRoutes_asOperator(t *testing.T) {
	t.Parallel()
	opNS := operatorOwner(t)
	for _, tc := range []struct {
		name string
		run  func(*testing.T, *ns.Namespace)
	}{
		{"NetworkRoutes_otherNamespaceCredentialRefused", networkRoutesOtherNamespaceCredentialRefused},
		{"NetworkConnect_malformedInputIs4xx", networkConnectMalformedInputIs4xx},
		{"NetworkConnect_hostileTargetsNotConnected", networkConnectHostileTargetsNotConnected},
		{"NetworkDisconnect_unheldPeerIdempotent", networkDisconnectUnheldPeerIdempotent},
		{"NetworkConnect_connectedPeerIdempotent", networkConnectConnectedPeerIdempotent},
		{"NetworkStatus_operatorSeesDocumentedShape", networkStatusOperatorSeesDocumentedShape},
		{"NetworkPeers_operatorListsMultiaddrs", networkPeersOperatorListsMultiaddrs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, opNS)
		})
	}
}
