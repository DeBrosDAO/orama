package rwagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultSocketName is the socket file relative to ~/.rootwallet/.
	DefaultSocketName = "agent.sock"

	// DefaultTimeout for HTTP requests to the agent.
	// Set high enough to allow pending approval flow (2 min approval timeout).
	DefaultTimeout = 150 * time.Second
)

// Client communicates with the rootwallet agent daemon over a Unix socket.
type Client struct {
	httpClient *http.Client
	socketPath string
}

// New creates a client that connects to the agent's Unix socket.
// If socketPath is empty, defaults to ~/.rootwallet/agent.sock.
func New(socketPath string) *Client {
	if socketPath == "" {
		home, _ := os.UserHomeDir()
		socketPath = filepath.Join(home, ".rootwallet", DefaultSocketName)
	}

	return &Client{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
			},
			Timeout: DefaultTimeout,
		},
	}
}

// Status returns the agent's current status.
func (c *Client) Status(ctx context.Context) (*StatusResponse, error) {
	var resp apiResponse[StatusResponse]
	if err := c.doJSON(ctx, "GET", "/v1/status", nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, 0)
	}
	return &resp.Data, nil
}

// IsRunning returns true if the agent is reachable.
func (c *Client) IsRunning(ctx context.Context) bool {
	_, err := c.Status(ctx)
	return err == nil
}

// GetSSHKey retrieves an SSH key from the vault.
// format: "priv", "pub", or "both".
func (c *Client) GetSSHKey(ctx context.Context, host, username, format string) (*VaultSSHData, error) {
	path := fmt.Sprintf("/v1/vault/ssh/%s/%s?format=%s",
		url.PathEscape(host),
		url.PathEscape(username),
		url.QueryEscape(format),
	)

	var resp apiResponse[VaultSSHData]
	if err := c.doJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, 0)
	}
	return &resp.Data, nil
}

// CreateSSHEntry creates a new SSH key entry in the vault.
func (c *Client) CreateSSHEntry(ctx context.Context, host, username string) (*VaultSSHData, error) {
	body := map[string]string{"host": host, "username": username}

	var resp apiResponse[VaultSSHData]
	if err := c.doJSON(ctx, "POST", "/v1/vault/ssh", body, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, 0)
	}
	return &resp.Data, nil
}

// GetPassword retrieves a stored password from the vault.
func (c *Client) GetPassword(ctx context.Context, domain, username string) (*VaultPasswordData, error) {
	path := fmt.Sprintf("/v1/vault/password/%s/%s",
		url.PathEscape(domain),
		url.PathEscape(username),
	)

	var resp apiResponse[VaultPasswordData]
	if err := c.doJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, 0)
	}
	return &resp.Data, nil
}

// GetAddress returns the active wallet address.
func (c *Client) GetAddress(ctx context.Context, chain string) (*WalletAddressData, error) {
	path := fmt.Sprintf("/v1/wallet/address?chain=%s", url.QueryEscape(chain))

	var resp apiResponse[WalletAddressData]
	if err := c.doJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, 0)
	}
	return &resp.Data, nil
}

// Sign signs a message with the wallet's private key.
// The desktop app may prompt the user for approval on first use.
func (c *Client) Sign(ctx context.Context, message, chain string) (*WalletSignData, error) {
	body := map[string]any{"message": message, "chain": chain}

	var resp apiResponse[WalletSignData]
	if err := c.doJSON(ctx, "POST", "/v1/wallet/sign", body, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, 0)
	}
	return &resp.Data, nil
}

// Unlock sends the password to unlock the agent.
func (c *Client) Unlock(ctx context.Context, password string, ttlMinutes int) error {
	body := map[string]any{"password": password, "ttlMinutes": ttlMinutes}

	var resp apiResponse[any]
	if err := c.doJSON(ctx, "POST", "/v1/unlock", body, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return c.apiError(resp.Error, resp.Code, 0)
	}
	return nil
}

// Lock locks the agent, zeroing all key material.
func (c *Client) Lock(ctx context.Context) error {
	var resp apiResponse[any]
	if err := c.doJSON(ctx, "POST", "/v1/lock", nil, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return c.apiError(resp.Error, resp.Code, 0)
	}
	return nil
}

// doJSON performs an HTTP request and decodes the JSON response.
func (c *Client) doJSON(ctx context.Context, method, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = strings.NewReader(string(data))
	}

	// URL host is ignored for Unix sockets, but required by http.NewRequest
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-RW-PID", strconv.Itoa(os.Getpid()))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Connection refused or socket not found = agent not running
		if isConnectionError(err) {
			return ErrAgentNotRunning
		}
		return fmt.Errorf("agent request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

func (c *Client) apiError(message, code string, statusCode int) *AgentError {
	return &AgentError{
		Code:       code,
		Message:    message,
		StatusCode: statusCode,
	}
}

// isConnectionError checks if the error is a connection-level failure.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such file or directory") ||
		strings.Contains(msg, "connect: no such file")
}
