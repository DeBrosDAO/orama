// Package noderesolver provides unified node discovery for the orama CLI.
//
// It resolves operator-owned nodes by querying the network's gateway API
// (primary) or falling back to the legacy nodes.conf file.
package noderesolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/cli"
	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// httpClient is the shared HTTP client for API calls.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// ResolveNodes returns the operator's nodes for a given environment.
// It first tries the network API (GET /v1/operator/nodes), then falls
// back to nodes.conf if the API is unreachable or returns no results.
func ResolveNodes(env string) ([]inspector.Node, error) {
	nodes, err := resolveFromNetwork(env)
	if err == nil && len(nodes) > 0 {
		return nodes, nil
	}

	// Fallback to nodes.conf
	confNodes, confErr := remotessh.LoadEnvNodes(env)
	if confErr != nil {
		if err != nil {
			return nil, fmt.Errorf("network API: %w; nodes.conf: %v", err, confErr)
		}
		return nil, confErr
	}
	return confNodes, nil
}

// ResolveNodesNetworkOnly queries only the network API without nodes.conf fallback.
func ResolveNodesNetworkOnly(env string) ([]inspector.Node, error) {
	return resolveFromNetwork(env)
}

// resolveFromNetwork queries the gateway API for operator-owned nodes.
func resolveFromNetwork(env string) ([]inspector.Node, error) {
	// 1. Get gateway URL for the environment
	gatewayURL, err := gatewayURLForEnv(env)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve gateway URL: %w", err)
	}

	// 2. Load stored credentials for this gateway
	apiKey, err := loadAPIKey(gatewayURL)
	if err != nil {
		return nil, fmt.Errorf("no credentials for %s: %w (run 'orama auth login' first)", gatewayURL, err)
	}

	return resolveFromNetworkWithURL(gatewayURL, apiKey, env)
}

// resolveFromNetworkWithURL queries a specific gateway URL with an API key.
// Exported for testing.
func resolveFromNetworkWithURL(gatewayURL, apiKey, env string) ([]inspector.Node, error) {
	endpoint := fmt.Sprintf("%s/v1/operator/nodes?env=%s", gatewayURL, url.QueryEscape(env))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach gateway: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Nodes []struct {
			ID          string `json:"id"`
			IPAddress   string `json:"ip_address"`
			InternalIP  string `json:"internal_ip"`
			Environment string `json:"environment"`
			Role        string `json:"role"`
			SSHUser     string `json:"ssh_user"`
			Status      string `json:"status"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	nodes := make([]inspector.Node, 0, len(result.Nodes))
	for _, n := range result.Nodes {
		user := n.SSHUser
		if user == "" {
			user = "root"
		}
		nodes = append(nodes, inspector.Node{
			Environment: n.Environment,
			User:        user,
			Host:        n.IPAddress,
			Role:        n.Role,
			VaultTarget: fmt.Sprintf("%s/%s", n.IPAddress, user),
		})
	}

	return nodes, nil
}

// gatewayURLForEnv returns the gateway URL for a given environment name.
// If env is empty, uses the active environment.
func gatewayURLForEnv(env string) (string, error) {
	if env == "" {
		e, err := cli.GetActiveEnvironment()
		if err != nil {
			return "", err
		}
		return e.GatewayURL, nil
	}

	e, err := cli.GetEnvironmentByName(env)
	if err != nil {
		return "", err
	}
	return e.GatewayURL, nil
}

// loadAPIKey loads the stored API key for a gateway URL.
func loadAPIKey(gatewayURL string) (string, error) {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}

	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil || creds.APIKey == "" {
		return "", fmt.Errorf("no credentials found for %s", gatewayURL)
	}

	return creds.APIKey, nil
}
