package ipfs

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// IPFS Cluster's REST API (constants.IPFSClusterAPIPort) pins, unpins and
// reads the whole cluster's pinset. It binds loopback, and that was all that
// guarded it: every process on the node — a tenant's deployment included —
// could unpin any tenant's content or pin anything into the cluster. It now
// requires HTTP basic auth (service.json api.restapi.basic_auth_credentials).
//
// The password is derived from the cluster secret rather than generated per
// node: install writes it into service.json from the same secret, and every
// consumer — gateways, orama-node — already holds that secret, so there is
// nothing new to distribute or keep in step. Anything that can derive it
// could already speak for the cluster.

// ClusterRESTUser is the one user the REST API accepts.
const ClusterRESTUser = "orama"

// clusterRESTPasswordPurpose is the HKDF domain separator for the password.
const clusterRESTPasswordPurpose = "ipfs-cluster-rest-api"

// ClusterRESTPassword derives the REST API password from the cluster secret.
// The secret is trimmed, as every other derivation from it is, so a trailing
// newline in one node's copy does not give it a different password.
func ClusterRESTPassword(clusterSecret string) (string, error) {
	key, err := secrets.DeriveKey(strings.TrimSpace(clusterSecret), clusterRESTPasswordPurpose)
	if err != nil {
		return "", fmt.Errorf("derive the IPFS Cluster REST API password: %w", err)
	}
	return hex.EncodeToString(key), nil
}

// clusterAuthTransport adds the REST API credentials to requests for the
// cluster API's host, and to nothing else: the same client talks to Kubo,
// which must not be handed the cluster's password.
type clusterAuthTransport struct {
	next     http.RoundTripper
	host     string
	password string
}

func (t *clusterAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != t.host {
		return t.next.RoundTrip(req)
	}
	authed := req.Clone(req.Context())
	authed.SetBasicAuth(ClusterRESTUser, t.password)
	return t.next.RoundTrip(authed)
}

// newClusterAuthTransport authenticates requests to clusterAPIURL's host with
// password.
func newClusterAuthTransport(clusterAPIURL, password string) (http.RoundTripper, error) {
	u, err := url.Parse(clusterAPIURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("IPFS Cluster API URL %q has no host to send credentials to", clusterAPIURL)
	}
	return &clusterAuthTransport{next: http.DefaultTransport, host: u.Host, password: password}, nil
}

// productionClusterSecretPath is the cluster secret on an installed node.
const productionClusterSecretPath = config.ProductionBaseDir + "/.orama/secrets/cluster-secret"

// LocalClusterRESTPassword is the REST API password on this node, for the
// operator commands that run on it as root (`orama node report`). The secret is
// read without following a symlink: the orama user owns the tree it is in.
func LocalClusterRESTPassword() (string, error) {
	raw, err := rootfs.At(config.ProductionBaseDir).ReadFile(productionClusterSecretPath, rootfs.SmallFileLimit)
	if err != nil {
		return "", fmt.Errorf("read the cluster secret the IPFS Cluster REST API password is derived from: %w", err)
	}
	return ClusterRESTPassword(string(raw))
}
