package invite

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/invite"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"gopkg.in/yaml.v3"
)

// Handle processes the invite command
// Options holds the flags for the invite command.
type Options struct {
	Expiry time.Duration
	// Raw prints only the encoded invite and a newline.
	Raw bool
}

// Run creates a new invite token.
func Run(opts Options) error {
	// Must run on a cluster node with RQLite running locally
	domain, publicIP, err := readNodeIdentity(config.ProductionNodeConfigPath)
	if err != nil {
		return clierr.NotFound("could not read the node config: %w\n"+
			"  Run this on an installed node", err)
	}

	// Generate random token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return clierr.Failure("failed to generate the token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	expiry := opts.Expiry
	if expiry <= 0 {
		expiry = time.Hour
	}

	expiresAt := time.Now().UTC().Add(expiry).Format("2006-01-02 15:04:05")

	// Get node ID for created_by
	nodeID := "unknown"
	if hostname, err := os.Hostname(); err == nil {
		nodeID = hostname
	}

	// Insert token into RQLite via HTTP API
	if err := insertToken(token, nodeID, expiresAt); err != nil {
		return clierr.Unavailable("failed to store the invite token: %w\n"+
			"  Make sure RQLite is running on this node", err)
	}

	// The invite names THIS node, by address, and the certificate it serves.
	// It used to name https://<domain>, which DNS spreads across every
	// nameserver — each serving a certificate of its own — so a joining node
	// that reached a different node than the one fingerprinted here failed its
	// pin. The fingerprint is read from this node's own listener, without
	// verification: the joining node's pin is the check, and a staging or
	// not-yet-trusted certificate must still be pinnable.
	joinURL := "https://" + publicIP
	fingerprint, err := invite.FingerprintServed(net.JoinHostPort(localhostIP, strconv.Itoa(httpsPort)), domain)
	if err != nil {
		return clierr.Unavailable("could not read this node's TLS certificate: %w\n"+
			"  The joining node needs its fingerprint to verify the cluster; check that Caddy has a certificate for %s", err, domain)
	}

	encoded, err := invite.Encode(invite.Invite{
		JoinURL:       joinURL,
		Token:         token,
		CAFingerprint: fingerprint,
		SNI:           domain,
	})
	if err != nil {
		return clierr.Failure("could not encode the invite: %w", err)
	}

	if opts.Raw {
		fmt.Println(encoded)
		return nil
	}

	fmt.Printf("\nInvite created (expires in %s)\n\n", expiry)
	fmt.Printf("Run this on the new node:\n\n")
	fmt.Printf("  sudo orama node install --token %s --vps-ip <NEW_NODE_IP> --nameserver\n\n", encoded)
	fmt.Printf("Replace <NEW_NODE_IP> with the new node's public IP address.\n")
	fmt.Printf("The invite carries the gateway to join and the certificate to pin,\n")
	fmt.Printf("so there is nothing else to copy across.\n")
	return nil
}

// localhostIP and httpsPort are where this node serves HTTPS to itself.
const (
	localhostIP = "127.0.0.1"
	httpsPort   = 443
)

// readNodeIdentity reads the node's domain and public address from node.yaml
// at configPath.
func readNodeIdentity(configPath string) (domain, publicIP string, err error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", "", fmt.Errorf("read config: %w", err)
	}

	var config struct {
		Node struct {
			Domain   string `yaml:"domain"`
			PublicIP string `yaml:"public_ip"`
		} `yaml:"node"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return "", "", fmt.Errorf("parse config: %w", err)
	}
	if config.Node.Domain == "" {
		return "", "", fmt.Errorf("node domain not set in config")
	}
	// node.yaml is the orama user's; the join URL a joining node trusts is
	// built from this, so it is checked, not taken as-is.
	if err := install.ValidatePublicIP(config.Node.PublicIP); err != nil {
		return "", "", fmt.Errorf("node.public_ip in %s is not usable (%v); run `orama node upgrade` on this node with this release, "+
			"which records it (add --public-ip <this node's public IP> if it cannot be detected)", configPath, err)
	}
	return config.Node.Domain, config.Node.PublicIP, nil
}

// insertToken inserts an invite token into RQLite via HTTP API using parameterized queries.
//
// What is stored is the hash. The token itself is printed to the operator once
// and exists nowhere else — a registry that holds a usable invite token holds a
// key to every secret the cluster has.
func insertToken(token, createdBy, expiresAt string) error {
	stmt := []interface{}{
		"INSERT INTO invite_tokens (token, created_by, expires_at) VALUES (?, ?, ?)",
		operator.HashInviteToken(token), createdBy, expiresAt,
	}
	payload, err := json.Marshal([]interface{}{stmt})
	if err != nil {
		return fmt.Errorf("failed to marshal query: %w", err)
	}

	ep, err := rqlite.LocalNodeEndpoint()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, ep.BaseURL()+"/db/execute", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build RQLite request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(ep.Username, ep.Password)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to RQLite at %s: %w", ep, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RQLite at %s returned status %d", ep, resp.StatusCode)
	}

	return nil
}
