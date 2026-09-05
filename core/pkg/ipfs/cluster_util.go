package ipfs

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// clusterSecretHexLen is the length of the shared secret as stored: 32 random
// bytes, hex-encoded.
const clusterSecretHexLen = 64

// loadOrGenerateClusterSecret returns the IPFS-Cluster shared secret, creating
// one ONLY when this node has never joined a cluster.
//
// The secret is the key to the cluster's libp2p private network: a node holding
// a different one completes no handshake with any peer, so its pins never
// replicate, in either direction. It reports healthy by every local check and
// ipfs-cluster logs only a generic connection failure.
//
// The previous version generated a new secret whenever the file could not be
// READ - EACCES, EIO, a transient mount problem, a file the join handshake had
// not written yet - or whenever the value was not exactly 64 characters, and it
// discarded the write error, so a failed write minted a different secret on
// every subsequent start. Three ways to silently partition a node from the
// cluster it belongs to.
//
// Generating is now the narrow case: only when the file is genuinely absent AND
// this node has no ipfs-cluster identity, which together mean it has never been
// part of a cluster. Anything else is fatal, because the alternative is to
// quietly invent a new network.
func loadOrGenerateClusterSecret(path string, clusterPath string) (string, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		secret := strings.TrimSpace(string(data))
		if len(secret) != clusterSecretHexLen {
			return "", fmt.Errorf(
				"cluster secret at %s is %d characters, expected %d: refusing to replace it, "+
					"because generating a new one would silently cut this node out of the cluster's private network. "+
					"Restore the value shared by the rest of the cluster (the join handshake distributes it)",
				path, len(secret), clusterSecretHexLen)
		}
		return secret, nil

	case !os.IsNotExist(err):
		// The file may well be correct; we simply could not read it. Inventing a
		// replacement is the one response guaranteed to be wrong.
		return "", fmt.Errorf("cannot read cluster secret at %s: %w "+
			"(refusing to generate a replacement: this node would join no one)", path, err)
	}

	// Absent. Safe to generate only if this node has never joined a cluster.
	if hasClusterIdentity(clusterPath) {
		return "", fmt.Errorf(
			"cluster secret at %s is missing, but this node already has an ipfs-cluster identity at %s. "+
				"It has joined a cluster before, so a fresh secret would partition it. "+
				"Restore the secret from another node (it is the same value fleet-wide)",
			path, clusterPath)
	}

	secret, err := generateRandomSecret()
	if err != nil {
		return "", fmt.Errorf("generate cluster secret: %w", err)
	}
	if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
		// Returning the secret after a failed write is how the old code produced
		// a DIFFERENT secret on every restart.
		return "", fmt.Errorf("write new cluster secret to %s: %w", path, err)
	}
	// Read it back: a write that lands somewhere unexpected, or is truncated, is
	// worth catching here rather than as an unexplained peering failure later.
	written, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("verify new cluster secret at %s: %w", path, err)
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(string(written))), []byte(secret)) != 1 {
		return "", fmt.Errorf("cluster secret at %s does not match what was just written", path)
	}
	return secret, nil
}

// hasClusterIdentity reports whether this node already holds an ipfs-cluster
// identity, i.e. it has been part of a cluster.
func hasClusterIdentity(clusterPath string) bool {
	if clusterPath == "" {
		return false
	}
	for _, name := range []string{"identity.json", "service.json"} {
		info, err := os.Stat(filepath.Join(clusterPath, name))
		if err == nil && info.Size() > 0 {
			return true
		}
	}
	return false
}

func generateRandomSecret() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func parseClusterPorts(rawURL string) (int, int, error) {
	if !strings.HasPrefix(rawURL, "http") {
		rawURL = "http://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return 9096, 9094, nil
	}
	_, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return 9096, 9094, nil
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	if port == 0 {
		return 9096, 9094, nil
	}
	return port + 2, port, nil
}

func parseIPFSPort(rawURL string) (int, error) {
	if !strings.HasPrefix(rawURL, "http") {
		rawURL = "http://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return constants.IPFSAPIPort, nil
	}
	_, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return constants.IPFSAPIPort, nil
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	if port == 0 {
		return constants.IPFSAPIPort, nil
	}
	return port, nil
}

func extractIPFromMultiaddrForCluster(maddr string) string {
	parts := strings.Split(maddr, "/")
	for i, part := range parts {
		if (part == "ip4" || part == "dns" || part == "dns4") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func extractDomainFromMultiaddr(maddr string) string {
	parts := strings.Split(maddr, "/")
	for i, part := range parts {
		if (part == "dns" || part == "dns4" || part == "dns6") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func newStandardHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
	}
}
