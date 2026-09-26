package ipfs

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// Kubo's RPC API binds loopback, and that was all that guarded it. A tenant
// deployment can open a TCP connection to 127.0.0.1 — the gateway reaches the
// deployment from there, so the unit cannot deny loopback — and the API is
// full control of the node: pin, unpin, connect, read any block. It now
// requires a bearer token (API.Authorizations). The token is derived from the
// cluster secret, like the Cluster REST password, with its own label so the
// two credentials are not interchangeable.

// kuboAPITokenPurpose is the HKDF domain separator for the Kubo RPC bearer.
const kuboAPITokenPurpose = "ipfs-kubo-api"

// KuboAPIUser is the one API.Authorizations entry install writes.
const KuboAPIUser = "orama"

// KuboAPIToken derives the bearer Kubo's RPC API requires. The secret is
// trimmed, as every other derivation from it is.
func KuboAPIToken(clusterSecret string) (string, error) {
	key, err := secrets.DeriveKey(strings.TrimSpace(clusterSecret), kuboAPITokenPurpose)
	if err != nil {
		return "", fmt.Errorf("derive the Kubo API token: %w", err)
	}
	return hex.EncodeToString(key), nil
}

// LocalKuboAPIToken is the bearer on this node, for commands that run on it
// as root. The secret is read without following a symlink.
func LocalKuboAPIToken() (string, error) {
	raw, err := rootfs.At(config.ProductionBaseDir).ReadFile(productionClusterSecretPath, rootfs.SmallFileLimit)
	if err != nil {
		return "", fmt.Errorf("read the cluster secret the Kubo API token is derived from: %w", err)
	}
	return KuboAPIToken(string(raw))
}

// PostAPI POSTs rawURL with the Kubo bearer. An empty token sends no header,
// which a configured node refuses.
func PostAPI(ctx context.Context, rawURL, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return http.DefaultClient.Do(req)
}

// LocalPostAPI POSTs a Kubo RPC on this node with the bearer derived from its
// cluster secret.
func LocalPostAPI(ctx context.Context, rawURL string) (*http.Response, error) {
	token, err := LocalKuboAPIToken()
	if err != nil {
		return nil, err
	}
	return PostAPI(ctx, rawURL, token)
}
