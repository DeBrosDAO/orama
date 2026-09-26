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
	"syscall"
	"time"
)

const (
	// DefaultSocketName is the socket file relative to ~/.rootwallet/.
	DefaultSocketName = "agent.sock"

	// AgentApprovalTimeout mirrors APPROVAL_TIMEOUT in the RootWallet agent:
	// how long it waits for someone to answer the approval prompt.
	AgentApprovalTimeout = 120 * time.Second

	// AgentUnlockWaitTimeout mirrors UNLOCK_WAIT_TIMEOUT in the agent: how long
	// a vault route waits for the wallet to be unlocked.
	AgentUnlockWaitTimeout = 120 * time.Second

	// clientTimeoutMargin keeps the client waiting slightly longer than the
	// agent can, so the agent's own typed error arrives instead of a context
	// deadline from this side.
	clientTimeoutMargin = 30 * time.Second

	// DefaultTimeout for HTTP requests to the agent.
	//
	// A first run against a locked wallet costs both agent timeouts in
	// sequence: check_permission waits up to AgentApprovalTimeout, then
	// get_metadata_key_or_wait waits up to AgentUnlockWaitTimeout. That is 240
	// seconds, and this used to be 150 — so the very case the timeout existed
	// for (approve, then unlock) died at 150s with a raw context error and no
	// hint about what had happened.
	DefaultTimeout = AgentApprovalTimeout + AgentUnlockWaitTimeout + clientTimeoutMargin
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
					if err := checkAgentSocket(socketPath); err != nil {
						return nil, err
					}
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
	status, err := c.doJSON(ctx, "GET", "/v1/status", nil, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, status)
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
	status, err := c.doJSON(ctx, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, status)
	}
	return &resp.Data, nil
}

// CreateSSHEntry creates a new SSH key entry in the vault.
func (c *Client) CreateSSHEntry(ctx context.Context, host, username string) (*VaultSSHData, error) {
	body := map[string]string{"host": host, "username": username}

	var resp apiResponse[VaultSSHData]
	status, err := c.doJSON(ctx, "POST", "/v1/vault/ssh", body, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, status)
	}
	return &resp.Data, nil
}

// DeleteSSHEntry removes a host's SSH key from the vault. An entry that does
// not exist is already in the state asked for, so NOT_FOUND is not an error.
func (c *Client) DeleteSSHEntry(ctx context.Context, host, username string) error {
	path := fmt.Sprintf("/v1/vault/ssh/%s/%s", url.PathEscape(host), url.PathEscape(username))

	var resp apiResponse[struct{}]
	status, err := c.doJSON(ctx, "DELETE", path, nil, &resp)
	if err != nil {
		return err
	}
	if resp.OK {
		return nil
	}
	aerr := c.apiError(resp.Error, resp.Code, status)
	if aerr.Code == CodeNotFound {
		return nil
	}
	return aerr
}

// GetPassword retrieves a stored password from the vault.
func (c *Client) GetPassword(ctx context.Context, domain, username string) (*VaultPasswordData, error) {
	path := fmt.Sprintf("/v1/vault/password/%s/%s",
		url.PathEscape(domain),
		url.PathEscape(username),
	)

	var resp apiResponse[VaultPasswordData]
	status, err := c.doJSON(ctx, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, status)
	}
	return &resp.Data, nil
}

// GetAddress returns the active wallet address.
func (c *Client) GetAddress(ctx context.Context, chain string) (*WalletAddressData, error) {
	path := fmt.Sprintf("/v1/wallet/address?chain=%s", url.QueryEscape(chain))

	var resp apiResponse[WalletAddressData]
	status, err := c.doJSON(ctx, "GET", path, nil, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, status)
	}
	return &resp.Data, nil
}

// Sign signs a message with the wallet's private key.
// The desktop app may prompt the user for approval on first use.
func (c *Client) Sign(ctx context.Context, message, chain string) (*WalletSignData, error) {
	body := map[string]any{"message": message, "chain": chain}

	var resp apiResponse[WalletSignData]
	status, err := c.doJSON(ctx, "POST", "/v1/wallet/sign", body, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, status)
	}
	return &resp.Data, nil
}

// PurposeOramaArchive scopes a signature to the Orama build-archive format.
// The agent signs a message in that format only for this purpose, and only
// for a caller granted wallet:sign:orama-archive; plain wallet:sign refuses it.
const PurposeOramaArchive = "orama-archive"

// SignForPurpose signs message under a domain-separated purpose. The agent
// checks that the message parses as that purpose's format and that the caller
// holds the purpose's own grant.
func (c *Client) SignForPurpose(ctx context.Context, message, chain, purpose string) (*WalletSignData, error) {
	body := map[string]any{"message": message, "chain": chain, "purpose": purpose}

	var resp apiResponse[WalletSignData]
	status, err := c.doJSON(ctx, "POST", "/v1/wallet/sign", body, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, status)
	}
	return &resp.Data, nil
}

// Unlock sends the password to unlock the agent.
func (c *Client) Unlock(ctx context.Context, password string, ttlMinutes int) error {
	body := map[string]any{"password": password, "ttlMinutes": ttlMinutes}

	var resp apiResponse[any]
	status, err := c.doJSON(ctx, "POST", "/v1/unlock", body, &resp)
	if err != nil {
		return err
	}
	if !resp.OK {
		return c.apiError(resp.Error, resp.Code, status)
	}
	return nil
}

// Lock locks the agent, zeroing all key material.
func (c *Client) Lock(ctx context.Context) error {
	var resp apiResponse[any]
	status, err := c.doJSON(ctx, "POST", "/v1/lock", nil, &resp)
	if err != nil {
		return err
	}
	if !resp.OK {
		return c.apiError(resp.Error, resp.Code, status)
	}
	return nil
}

// doJSON performs an HTTP request and decodes the JSON response. It returns the
// HTTP status so the caller can tell apart two answers that share a code: the
// agent reports a locked wallet as 423 after waiting, and as 401 when it
// refuses without waiting.
func (c *Client) doJSON(ctx context.Context, method, path string, body any, result any) (int, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = strings.NewReader(string(data))
	}

	// URL host is ignored for Unix sockets, but required by http.NewRequest
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bodyReader)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-RW-PID", strconv.Itoa(os.Getpid()))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Connection refused or socket not found = agent not running
		if isConnectionError(err) {
			return 0, ErrAgentNotRunning
		}
		return 0, fmt.Errorf("agent request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	if err := json.Unmarshal(data, result); err != nil {
		// A body that is not the agent's JSON envelope is still an answer, and
		// the status says what kind. Reporting every one of these as "decode
		// response" hid a 413 behind a JSON error.
		return resp.StatusCode, &AgentError{
			Code:       codeForStatus(resp.StatusCode),
			Message:    summarize(data, resp.StatusCode),
			StatusCode: resp.StatusCode,
		}
	}

	return resp.StatusCode, nil
}

// codeForStatus names an error the agent did not name itself.
func codeForStatus(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusLocked:
		return CodeAgentLocked
	case http.StatusForbidden:
		return CodePermissionDenied
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case http.StatusBadRequest:
		return CodeInvalidRequest
	default:
		return CodeInternalError
	}
}

// summarize turns a non-JSON body into one line, without pasting an arbitrary
// amount of the agent's output into an error message.
func summarize(body []byte, status int) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return fmt.Sprintf("agent returned HTTP %d with an empty body", status)
	}
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	const limit = 200
	if len(text) > limit {
		text = text[:limit] + "…"
	}
	return fmt.Sprintf("agent returned HTTP %d: %s", status, text)
}

func (c *Client) apiError(message, code string, statusCode int) *AgentError {
	return &AgentError{
		Code:       code,
		Message:    message,
		StatusCode: statusCode,
	}
}

// checkAgentSocket refuses to dial a socket another user could have planted
// or could read. A missing path is returned as-is so the caller still treats
// it as the agent not running.
func checkAgentSocket(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("rootwallet agent socket %s has no owner", path)
	}
	return agentSocketAllowed(fi.Mode(), int(st.Uid), os.Getuid())
}

// agentSocketAllowed is the rule checkAgentSocket applies: a real socket,
// owned by the caller, and not group- or world-accessible.
func agentSocketAllowed(mode os.FileMode, owner, caller int) error {
	if mode&os.ModeSymlink != 0 {
		return fmt.Errorf("rootwallet agent socket is a symlink")
	}
	if mode&os.ModeSocket == 0 {
		return fmt.Errorf("rootwallet agent socket is not a socket")
	}
	if owner != caller {
		return fmt.Errorf("rootwallet agent socket is owned by uid %d", owner)
	}
	if mode.Perm()&0o077 != 0 {
		return fmt.Errorf("rootwallet agent socket is group- or world-accessible (mode %o)", mode.Perm())
	}
	return nil
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
