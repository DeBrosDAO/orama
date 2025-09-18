package client

import (
	"os"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/multiformats/go-multiaddr"
)

// DefaultBootstrapPeers returns the library's default bootstrap peer multiaddrs.
// These can be overridden by environment variables or config.
func DefaultBootstrapPeers() []string {
	defaultCfg := config.DefaultConfig()
	return defaultCfg.Discovery.BootstrapPeers
}

// DefaultDatabaseEndpoints returns default DB HTTP endpoints.
// These can be overridden by environment variables or config.
func DefaultDatabaseEndpoints() []string {
	// Check environment variable first
	if envNodes := os.Getenv("RQLITE_NODES"); envNodes != "" {
		return normalizeEndpoints(splitCSVOrSpace(envNodes))
	}

	// Get default port from environment or use port from config
	defaultCfg := config.DefaultConfig()
	port := defaultCfg.Database.RQLitePort
	if envPort := os.Getenv("RQLITE_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
			port = p
		}
	}

	// Try to derive from bootstrap peers if available
	peers := DefaultBootstrapPeers()
	if len(peers) > 0 {
		endpoints := make([]string, 0, len(peers))
		for _, s := range peers {
			ma, err := multiaddr.NewMultiaddr(s)
			if err != nil {
				continue
			}
			endpoints = append(endpoints, endpointFromMultiaddr(ma, port))
		}
		return dedupeStrings(endpoints)
	}

	// Fallback to localhost
	return []string{"http://localhost:" + strconv.Itoa(port)}
}

// MapAddrsToDBEndpoints converts a set of peer multiaddrs to DB HTTP endpoints using dbPort.
func MapAddrsToDBEndpoints(addrs []multiaddr.Multiaddr, dbPort int) []string {
	if dbPort <= 0 {
		dbPort = 5001
	}
	eps := make([]string, 0, len(addrs))
	for _, ma := range addrs {
		eps = append(eps, endpointFromMultiaddr(ma, dbPort))
	}
	return dedupeStrings(eps)
}

// endpointFromMultiaddr extracts host from multiaddr and creates HTTP endpoint
func endpointFromMultiaddr(ma multiaddr.Multiaddr, port int) string {
	var host string

	// Prefer DNS if present, then IP
	if v, err := ma.ValueForProtocol(multiaddr.P_DNS); err == nil && v != "" {
		host = v
	}
	if host == "" {
		if v, err := ma.ValueForProtocol(multiaddr.P_DNS4); err == nil && v != "" {
			host = v
		}
	}
	if host == "" {
		if v, err := ma.ValueForProtocol(multiaddr.P_DNS6); err == nil && v != "" {
			host = v
		}
	}
	if host == "" {
		if v, err := ma.ValueForProtocol(multiaddr.P_IP4); err == nil && v != "" {
			host = v
		}
	}
	if host == "" {
		if v, err := ma.ValueForProtocol(multiaddr.P_IP6); err == nil && v != "" {
			host = "[" + v + "]" // IPv6 needs brackets in URLs
		}
	}
	if host == "" {
		host = "localhost"
	}

	return "http://" + host + ":" + strconv.Itoa(port)
}

// normalizeEndpoints ensures each endpoint has an http scheme and a port (defaults to 5001)
func normalizeEndpoints(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}

		// Prepend scheme if missing
		if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
			s = "http://" + s
		}

		// Simple check for port (doesn't handle all cases but good enough)
		if !strings.Contains(s, ":5001") && !strings.Contains(s, ":500") && !strings.Contains(s, ":501") {
			// Check if there's already a port after the host
			parts := strings.Split(s, "://")
			if len(parts) == 2 {
				hostPart := parts[1]
				// Count colons to detect port (simple heuristic)
				colonCount := strings.Count(hostPart, ":")
				if colonCount == 0 || (strings.Contains(hostPart, "[") && colonCount == 1) {
					// No port found, add default
					s = s + ":5001"
				}
			}
		}

		out = append(out, s)
	}
	return out
}

// dedupeStrings removes duplicate strings from slice
func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return in
	}

	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}

	return out
}

// splitCSVOrSpace splits a string by commas or spaces
func splitCSVOrSpace(s string) []string {
	// Replace commas with spaces, then split on spaces
	s = strings.ReplaceAll(s, ",", " ")
	fields := strings.Fields(s)
	return fields
}

// truthy reports if s is a common truthy string
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
