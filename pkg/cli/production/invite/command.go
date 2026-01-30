package invite

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Handle processes the invite command
func Handle(args []string) {
	// Must run on a cluster node with RQLite running locally
	domain, err := readNodeDomain()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not read node config: %v\n", err)
		fmt.Fprintf(os.Stderr, "Make sure you're running this on an installed node.\n")
		os.Exit(1)
	}

	// Generate random token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating token: %v\n", err)
		os.Exit(1)
	}
	token := hex.EncodeToString(tokenBytes)

	// Determine expiry (default 1 hour, --expiry flag for override)
	expiry := time.Hour
	for i, arg := range args {
		if arg == "--expiry" && i+1 < len(args) {
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid expiry duration: %v\n", err)
				os.Exit(1)
			}
			expiry = d
		}
	}

	expiresAt := time.Now().UTC().Add(expiry).Format("2006-01-02 15:04:05")

	// Get node ID for created_by
	nodeID := "unknown"
	if hostname, err := os.Hostname(); err == nil {
		nodeID = hostname
	}

	// Insert token into RQLite via HTTP API
	if err := insertToken(token, nodeID, expiresAt); err != nil {
		fmt.Fprintf(os.Stderr, "Error storing invite token: %v\n", err)
		fmt.Fprintf(os.Stderr, "Make sure RQLite is running on this node.\n")
		os.Exit(1)
	}

	// Print the invite command
	fmt.Printf("\nInvite token created (expires in %s)\n\n", expiry)
	fmt.Printf("Run this on the new node:\n\n")
	fmt.Printf("  sudo orama install --join https://%s --token %s --vps-ip <NEW_NODE_IP> --nameserver\n\n", domain, token)
	fmt.Printf("Replace <NEW_NODE_IP> with the new node's public IP address.\n")
}

// readNodeDomain reads the domain from the node config file
func readNodeDomain() (string, error) {
	configPath := "/home/debros/.orama/configs/node.yaml"
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

// insertToken inserts an invite token into RQLite via HTTP API
func insertToken(token, createdBy, expiresAt string) error {
	body := fmt.Sprintf(`[["INSERT INTO invite_tokens (token, created_by, expires_at) VALUES ('%s', '%s', '%s')"]]`,
		token, createdBy, expiresAt)

	req, err := http.NewRequest("POST", "http://localhost:5001/db/execute", strings.NewReader(body))
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
