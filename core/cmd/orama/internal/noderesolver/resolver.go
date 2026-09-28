// Package noderesolver provides unified node discovery for the orama CLI.
//
// It resolves operator-owned nodes from the gateway API, then from the
// machines setup recorded on the environment, then from nodes.conf.
package noderesolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// resolverTimeout bounds one node-list request.
const resolverTimeout = 10 * time.Second

// httpClient is the shared HTTP client for API calls. It follows no redirect:
// the request carries an operator bearer, which Go would forward on a redirect
// to a subdomain (a tenant's app) or to plain http.
var httpClient = &http.Client{
	Timeout:       resolverTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// ResolveNodes returns the operator's nodes for a given environment.
// It first tries the network API (GET /v1/operator/nodes), then falls
// back to nodes.conf if the API is unreachable or returns no results.
func ResolveNodes(env string) ([]inspector.Node, error) {
	nodes, err := resolveFromNetwork(env)
	conf, confErr := loadConfNodes(env)
	return chooseNodes(nodes, err, recordedNodes(env), conf, confErr)
}

// ResolveNodesNetworkOnly queries only the network API without nodes.conf fallback.
func ResolveNodesNetworkOnly(env string) ([]inspector.Node, error) {
	return resolveFromNetwork(env)
}

// resolveFromNetwork queries the gateway API for operator-owned nodes.
func resolveFromNetwork(env string) ([]inspector.Node, error) {
	// 1. Get gateway URL for the environment
	gatewayURL, err := GatewayURLForEnv(env)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve gateway URL: %w", err)
	}

	// 2. Load stored credentials for this gateway
	token, err := LoadBearer(gatewayURL)
	if err != nil {
		return nil, fmt.Errorf("no credentials for %s: %w (run 'orama auth login' first)", gatewayURL, err)
	}

	return resolveFromNetworkWithURL(gatewayURL, token, env)
}

// resolveFromNetworkWithURL queries a specific gateway URL with a credential.
// Exported for testing.
func resolveFromNetworkWithURL(gatewayURL, token, env string) ([]inspector.Node, error) {
	endpoint := fmt.Sprintf("%s/v1/operator/nodes?env=%s", gatewayURL, url.QueryEscape(env))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

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
		node := NewNode(n.IPAddress, n.SSHUser, n.Environment)
		node.Role = n.Role
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// sandboxVaultTarget is the single SSH key every ephemeral sandbox node shares.
const sandboxVaultTarget = "sandbox/root"

// defaultSSHUser is who the CLI logs in as when the inventory names no user.
const defaultSSHUser = "root"

// NewNode builds an inspector.Node for a host, filling in the SSH user and the
// vault target that says which wallet key opens it. Sandbox nodes are created
// from one shared key; every other node has a key of its own, keyed by host and
// user. Callers that reach a machine the inventory does not know about yet --
// `orama push --host` seeding a fresh node -- go through here too, so a target
// is addressed the same way whether or not it has been registered.
func NewNode(host, user, env string) inspector.Node {
	if user == "" {
		user = defaultSSHUser
	}
	vaultTarget := fmt.Sprintf("%s/%s", host, user)
	if env == "sandbox" {
		vaultTarget = sandboxVaultTarget
	}
	return inspector.Node{
		Environment: env,
		User:        user,
		Host:        host,
		VaultTarget: vaultTarget,
	}
}

// GatewayURLForEnv returns the gateway URL for a given environment name.
// If env is empty, uses the active environment.
func GatewayURLForEnv(env string) (string, error) {
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

// LoadBearer returns a short-lived credential for a gateway, renewing the
// stored session if it has to. Every operator API call the CLI makes — node
// resolution here, `orama monitor`'s telemetry — authenticates this way.
func LoadBearer(gatewayURL string) (string, error) {
	store, err := auth.LoadEnhancedCredentials()
	if err != nil {
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}

	creds := store.GetDefaultCredential(gatewayURL)
	if creds == nil {
		return "", fmt.Errorf("no credentials found for %s", gatewayURL)
	}
	return auth.Bearer(gatewayURL, store, creds)
}
