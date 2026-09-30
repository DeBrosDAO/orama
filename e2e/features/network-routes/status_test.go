//go:build e2e_fleet

package networkroutes

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// statusFields is every member GET /v1/network/status may carry
// (core/pkg/client/interface.go NetworkStatus): the peer ids and the storage
// peers' addresses docs/API_SURFACE.md says are the operator's.
var statusFields = []string{"node_id", "peer_id", "connected", "peer_count", "database_size", "uptime", "ipfs", "ipfs_cluster"}

// loopbackMarks are the addresses the status filters out of the storage
// peers' lists before handing them to another node (network_client.go).
var loopbackMarks = []string{"/ip4/127.0.0.1/", "/ip6/::1/"}

// networkStatus is the part of the status the test asserts on.
type networkStatus struct {
	PeerID    string `json:"peer_id"`
	Connected bool   `json:"connected"`
	PeerCount int    `json:"peer_count"`
	IPFS      *struct {
		SwarmAddresses []string `json:"swarm_addresses"`
	} `json:"ipfs"`
	IPFSCluster *struct {
		Addresses []string `json:"addresses"`
	} `json:"ipfs_cluster"`
}

// networkStatusOperatorSeesDocumentedShape: an operator reads each
// node's status: 200, only the documented members, a peer id, connected to
// at least one peer, storage addresses without loopback, and nothing the
// run's redactor recognises as a secret or credential.
func networkStatusOperatorSeesDocumentedShape(t *testing.T, opNS *ns.Namespace) {
	f := harness.Fleet(t)
	op := tenancy.Owner(opNS)
	for _, node := range f.State.Nodes {
		resp := call(t, harness.GW(t).PinTo(node.PublicIP), pathStatus, op, nil, nil).Expect(t, http.StatusOK)
		var members map[string]json.RawMessage
		var st networkStatus
		if err := json.Unmarshal(resp.Body, &members); err != nil {
			t.Fatalf("%s: status is not a JSON object: %v", node.Name, err)
		}
		if err := json.Unmarshal(resp.Body, &st); err != nil {
			t.Fatalf("%s: status does not decode: %v", node.Name, err)
		}
		for k := range members {
			if !slices.Contains(statusFields, k) {
				t.Errorf("%s: undocumented member %q in the network status", node.Name, k)
			}
		}
		if st.PeerID == "" || !st.Connected || st.PeerCount < 1 {
			t.Errorf("%s: peer_id %q connected %v peer_count %d, want a peer id, connected, at least one peer",
				node.Name, st.PeerID, st.Connected, st.PeerCount)
		}
		requireNoLoopback(t, node.Name, st)
		if body := string(resp.Body); f.Redact(body) != body {
			t.Errorf("%s: the network status carries a secret or credential", node.Name)
		}
	}
}

// requireNoLoopback records an error for a loopback storage address.
func requireNoLoopback(t *testing.T, node string, st networkStatus) {
	t.Helper()
	var addrs []string
	if st.IPFS != nil {
		addrs = append(addrs, st.IPFS.SwarmAddresses...)
	}
	if st.IPFSCluster != nil {
		addrs = append(addrs, st.IPFSCluster.Addresses...)
	}
	for _, a := range addrs {
		for _, mark := range loopbackMarks {
			if strings.HasPrefix(a+"/", mark) {
				t.Errorf("%s: the status hands out a loopback storage address %s", node, a)
			}
		}
	}
}

// networkPeersOperatorListsMultiaddrs: an operator reads each node's
// peers: 200 with `peers` and nothing else, the gateway's own host first as
// /p2p/<id>, at least one remote peer, and every entry a multiaddr.
func networkPeersOperatorListsMultiaddrs(t *testing.T, opNS *ns.Namespace) {
	f := harness.Fleet(t)
	op := tenancy.Owner(opNS)
	for _, node := range f.State.Nodes {
		c := harness.GW(t).PinTo(node.PublicIP)
		var members map[string]json.RawMessage
		if err := call(t, c, pathPeers, op, nil, nil).Expect(t, http.StatusOK).Decode(&members); err != nil {
			t.Fatalf("%s: peers is not a JSON object: %v", node.Name, err)
		}
		if _, ok := members["peers"]; !ok || len(members) != 1 {
			t.Errorf("%s: peers carries %d members, want exactly \"peers\"", node.Name, len(members))
		}
		list := peersList(t, c, op)
		if len(list) == 0 || !strings.HasPrefix(list[0], "/p2p/") {
			t.Fatalf("%s: the list does not start with the gateway's own /p2p/ id: %v", node.Name, list)
		}
		if len(remotePeers(list)) == 0 {
			t.Errorf("%s: the gateway is connected to no peer: %v", node.Name, list)
		}
		for _, entry := range list {
			if !strings.HasPrefix(entry, "/") || strings.ContainsAny(entry, " \t\n") {
				t.Errorf("%s: %q is not a multiaddr", node.Name, entry)
			}
		}
	}
}
