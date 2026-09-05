package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/node/coreapi"
)

// coreAPIClient is how this node records itself in the core cluster.
//
// It is built on first use rather than at construction, because it needs two
// things a node does not have until it has joined: its peer id, and the cluster
// secret the join writes to disk.
//
// The client is remembered; a failure to build one is not. Building it reads a
// file, and a file being unreadable is exactly the kind of thing an operator
// fixes while the process is running — a wrong owner, a restored secret, a late
// mount. The boot supervisor is built to converge on that: it retries forever
// and never gives up on a component. Caching the error would defeat it, and the
// cost of that is not an error message — a node that never registers is reaped
// to `inactive` after 120 seconds and has its DNS records deleted, until
// somebody restarts it.
func (n *Node) coreAPIClient() (*coreapi.Client, error) {
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
	secret, err := n.clusterSecret()
	if err != nil {
		return nil, err
	}
	client, err := coreapi.New(constants.LocalGatewayURL(), nodeID, secret)
	if err != nil {
		return nil, err
	}
	n.coreAPI = client
	return n.coreAPI, nil
}

// clusterSecret reads the secret this node shares with the rest of the cluster.
func (n *Node) clusterSecret() (string, error) {
	dataDir, err := config.ExpandPath(n.config.Node.DataDir)
	if err != nil {
		return "", fmt.Errorf("expand the node data directory: %w", err)
	}
	path := filepath.Join(filepath.Dir(dataDir), "secrets", "cluster-secret")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the cluster secret at %s, which the join writes: %w", path, err)
	}
	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return "", fmt.Errorf("the cluster secret at %s is empty, so this node cannot prove which node it is", path)
	}
	return secret, nil
}
