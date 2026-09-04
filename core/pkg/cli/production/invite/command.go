package invite

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"gopkg.in/yaml.v3"
)

// Handle processes the invite command
// Options holds the flags for the invite command.
type Options struct {
	Expiry time.Duration
}

// Run creates a new invite token.
func Run(opts Options) error {
	// Must run on a cluster node with RQLite running locally
	domain, err := readNodeDomain()
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

	// Get TLS certificate fingerprint for TOFU verification
	certFingerprint := getTLSCertFingerprint(domain)

	// Print the invite command
	fmt.Printf("\nInvite token created (expires in %s)\n\n", expiry)
	fmt.Printf("Run this on the new node:\n\n")
	if certFingerprint != "" {
		fmt.Printf("  sudo orama node install --join https://%s --token %s --ca-fingerprint %s --vps-ip <NEW_NODE_IP> --nameserver\n\n", domain, token, certFingerprint)
	} else {
		fmt.Printf("  sudo orama node install --join https://%s --token %s --vps-ip <NEW_NODE_IP> --nameserver\n\n", domain, token)
	}
	fmt.Printf("Replace <NEW_NODE_IP> with the new node's public IP address.\n")
	return nil
}

// getTLSCertFingerprint connects to the domain over TLS and returns the
// SHA-256 fingerprint of the leaf certificate. Returns empty string on failure.
func getTLSCertFingerprint(domain string) string {
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 5 * time.Second},
		"tcp",
		domain+":443",
		&tls.Config{MinVersion: tls.VersionTLS12},
	)
	if err != nil {
		return ""
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return ""
	}

	hash := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(hash[:])
}

// readNodeDomain reads the domain from the node config file
func readNodeDomain() (string, error) {
	configPath := "/opt/orama/.orama/configs/node.yaml"
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read config: %w", err)
	}

	var config struct {
		Node struct {
			Domain string `yaml:"domain"`
		} `yaml:"node"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("parse config: %w", err)
	}

	if config.Node.Domain == "" {
		return "", fmt.Errorf("node domain not set in config")
	}

	return config.Node.Domain, nil
}

// insertToken inserts an invite token into RQLite via HTTP API using parameterized queries
func insertToken(token, createdBy, expiresAt string) error {
	stmt := []interface{}{
		"INSERT INTO invite_tokens (token, created_by, expires_at) VALUES (?, ?, ?)",
		token, createdBy, expiresAt,
	}
	payload, err := json.Marshal([]interface{}{stmt})
	if err != nil {
		return fmt.Errorf("failed to marshal query: %w", err)
	}

	req, err := http.NewRequest("POST", constants.LocalRQLiteURL()+"/db/execute", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to RQLite: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("RQLite returned status %d", resp.StatusCode)
	}

	return nil
}
