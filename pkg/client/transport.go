package client

import (
	"net/http"
	"strings"
)

// getGatewayURL returns the gateway URL from config, defaulting to localhost:6001
func getGatewayURL(c *Client) string {
	cfg := c.Config()
	if cfg != nil && cfg.GatewayURL != "" {
		return strings.TrimSuffix(cfg.GatewayURL, "/")
	}
	return "http://localhost:6001"
}

// addAuthHeaders adds authentication headers to the request
func addAuthHeaders(req *http.Request, c *Client) {
	cfg := c.Config()
	if cfg == nil {
		return
	}

	// Prefer JWT if available
	if cfg.JWT != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.JWT)
		return
	}

	// Fallback to API key
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		req.Header.Set("X-API-Key", cfg.APIKey)
	}
}
