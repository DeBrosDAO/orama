package node

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/node/coreapi"
	"github.com/libp2p/go-libp2p/core/crypto"
)

// coreAPIClient is how this node records itself in the core cluster.
//
// It is built on first use rather than at construction, because it needs a
// libp2p identity this node does not have until it has started one.
//
// The client is remembered; a failure to build one is not. Building it reads a
// file and calls the local gateway, and both fail for reasons that get fixed
// while the process runs — a wrong owner, a gateway still coming up. The boot
// supervisor is built to converge on that: it retries forever and never gives
// up on a component. Caching the error would defeat it, and the cost of that is
// not an error message — a node that never registers is reaped to `inactive`
// after 120 seconds and has its DNS records deleted, until somebody restarts
// it.
func (n *Node) coreAPIClient(ctx context.Context) (*coreapi.Client, error) {
	n.coreAPIMu.Lock()
	defer n.coreAPIMu.Unlock()
	if n.coreAPI != nil {
		return n.coreAPI, nil
	}

	// A node that serves no gateway has nowhere to record itself — and should
	// not be advertising itself as active in any case, since a `dns_nodes` row
	// saying `active` promises that this node terminates TLS and proxies
	// tenants.
	if !n.config.HTTPGateway.Enabled {
		return nil, fmt.Errorf("this node runs no gateway (http_gateway.enabled is false), " +
			"so it cannot record itself and must not advertise itself as active")
	}

	nodeID := n.GetPeerID()
	if nodeID == "" {
		return nil, fmt.Errorf("node peer ID not available")
	}
	// The libp2p key behind that peer id. It is what proves to the cluster
	// which node an enrolment is for, and it is held only by this machine.
	identity := n.identityKey()
	if identity == nil {
		return nil, fmt.Errorf("this node's libp2p identity is not available, so it cannot prove which node it is")
	}
	oramaDir, err := n.oramaDir()
	if err != nil {
		return nil, err
	}
	own, err := auth.LoadOrCreateNodeKey(oramaDir)
	if err != nil {
		return nil, err
	}
	client, err := coreapi.New(constants.LocalGatewayURL(), nodeID, own, identity)
	if err != nil {
		return nil, err
	}

	// Recording the key is the first thing this node does, because every other
	// call is signed with it and the cluster accepts nothing else. A failure
	// here is a failure to build the client: a client whose key the cluster
	// does not have is one whose every request is refused.
	if err := client.EnrolKey(ctx); err != nil {
		return nil, fmt.Errorf("this node could not record its own key with the cluster: %w", err)
	}

	n.coreAPI = client
	return n.coreAPI, nil
}

// identityKey is the private half of this node's libp2p identity.
func (n *Node) identityKey() crypto.PrivKey {
	host := n.hostRef()
	if host == nil {
		return nil
	}
	return host.Peerstore().PrivKey(host.ID())
}

// oramaDir is the install root: the parent of the node's data directory.
func (n *Node) oramaDir() (string, error) {
	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return "", fmt.Errorf("expand the node data directory: %w", err)
	}
	return filepath.Dir(dataDir), nil
}
