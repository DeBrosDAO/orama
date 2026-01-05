package ipfs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func loadOrGenerateClusterSecret(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		secret := strings.TrimSpace(string(data))
		if len(secret) == 64 {
			return secret, nil
		}
	}

	secret, err := generateRandomSecret()
	if err != nil {
		return "", err
	}

	_ = os.WriteFile(path, []byte(secret), 0600)
	return secret, nil
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
		return 5001, nil
	}
	_, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return 5001, nil
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	if port == 0 {
		return 5001, nil
	}
	return port, nil
}

func parsePeerHostAndPort(multiaddr string) (string, int) {
	parts := strings.Split(multiaddr, "/")
	var hostStr string
	var port int
	for i, part := range parts {
		if part == "ip4" || part == "dns" || part == "dns4" {
			hostStr = parts[i+1]
		} else if part == "tcp" {
			fmt.Sscanf(parts[i+1], "%d", &port)
		}
	}
	return hostStr, port
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

