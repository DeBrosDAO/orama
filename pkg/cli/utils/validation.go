package utils

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/multiformats/go-multiaddr"
)

// ValidateGeneratedConfig loads and validates the generated node configuration
func ValidateGeneratedConfig(oramaDir string) error {
	configPath := filepath.Join(oramaDir, "configs", "node.yaml")

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("configuration file not found at %s", configPath)
	}

	// Load the config file
	file, err := os.Open(configPath)
	if err != nil {
		return fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var cfg config.Config
	if err := config.DecodeStrict(file, &cfg); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	// Validate the configuration
	if errs := cfg.Validate(); len(errs) > 0 {
		var errMsgs []string
		for _, e := range errs {
			errMsgs = append(errMsgs, e.Error())
		}
		return fmt.Errorf("configuration validation errors:\n  - %s", strings.Join(errMsgs, "\n  - "))
	}

	return nil
}

// ValidateDNSRecord validates that the domain points to the expected IP address
// Returns nil if DNS is valid, warning message if DNS doesn't match but continues,
// or error if DNS lookup fails completely
func ValidateDNSRecord(domain, expectedIP string) error {
	if domain == "" {
		return nil // No domain provided, skip validation
	}

	ips, err := net.LookupIP(domain)
	if err != nil {
		// DNS lookup failed - this is a warning, not a fatal error
		// The user might be setting up DNS after installation
		fmt.Printf("  ⚠️  DNS lookup failed for %s: %v\n", domain, err)
		fmt.Printf("     Make sure DNS is configured before enabling HTTPS\n")
		return nil
	}

	// Check if any resolved IP matches the expected IP
	for _, ip := range ips {
		if ip.String() == expectedIP {
			fmt.Printf("  ✓ DNS validated: %s → %s\n", domain, expectedIP)
			return nil
		}
	}

	// DNS doesn't point to expected IP - warn but continue
	resolvedIPs := make([]string, len(ips))
	for i, ip := range ips {
		resolvedIPs[i] = ip.String()
	}
	fmt.Printf("  ⚠️  DNS mismatch: %s resolves to %v, expected %s\n", domain, resolvedIPs, expectedIP)
	fmt.Printf("     HTTPS certificate generation may fail until DNS is updated\n")
	return nil
}

// NormalizePeers normalizes and validates peer multiaddrs
func NormalizePeers(peersStr string) ([]string, error) {
	if peersStr == "" {
		return nil, nil
	}

	// Split by comma and trim whitespace
	rawPeers := strings.Split(peersStr, ",")
	peers := make([]string, 0, len(rawPeers))
	seen := make(map[string]bool)

	for _, peer := range rawPeers {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			continue
		}

		// Validate multiaddr format
		if _, err := multiaddr.NewMultiaddr(peer); err != nil {
			return nil, fmt.Errorf("invalid multiaddr %q: %w", peer, err)
		}

		// Deduplicate
		if !seen[peer] {
			peers = append(peers, peer)
			seen[peer] = true
		}
	}

	return peers, nil
}

