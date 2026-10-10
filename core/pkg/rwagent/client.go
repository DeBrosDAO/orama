package rwagent

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"github.com/ethereum/go-ethereum/crypto"
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
	// warn receives the one-line warnings a background operation cannot
	// return as an error (KeepUnlocked). Stderr outside tests.
	warn io.Writer
	// guardErr refuses every request of an e2e run pointed at the real
	// wallet (e2eguard.go); nil otherwise.
	guardErr error
}

// New creates a client that connects to the agent's Unix socket.
// If socketPath is empty, defaults to ~/.rootwallet/agent.sock, except under
// ORAMA_E2E=1, where every request fails instead (see e2eGuard).
func New(socketPath string) *Client {
	requested := socketPath
	guardErr := e2eGuard(requested)
	if socketPath == "" {
		home, _ := os.UserHomeDir()
		socketPath = filepath.Join(home, ".rootwallet", DefaultSocketName)
	}

	return &Client{
		socketPath: socketPath,
		warn:       os.Stderr,
		guardErr:   guardErr,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					// The guard runs again at every dial: a path that was
					// safe when the client was made may since have been
					// swapped for a link into the real wallet.
					if err := e2eGuard(requested); err != nil {
						return nil, err
					}
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

// ErrTouchUnsupported means the agent predates POST /v1/touch, so nothing can
// hold its auto-lock window open.
var ErrTouchUnsupported = errors.New("the RootWallet agent has no /v1/touch")

// Touch resets the agent's auto-lock window. Only an approved app may call it
// (CodeNotApproved otherwise); it never unlocks a locked wallet.
func (c *Client) Touch(ctx context.Context) (*TouchResponse, error) {
	var resp apiResponse[TouchResponse]
	status, err := c.doJSON(ctx, "POST", "/v1/touch", nil, &resp)
	// Any 404 means the agent has no such route, whether it answered with its
	// JSON envelope or a plain-text not-found body. doJSON reports 0 for a
	// connection failure, so an agent that isn't running is never misread here.
	if status == http.StatusNotFound {
		return nil, fmt.Errorf("touch: %w", ErrTouchUnsupported)
	}
	// Only an approved app may touch, so a 403 means not approved even when its
	// body isn't the JSON envelope the agent normally sends.
	if status == http.StatusForbidden && (err != nil || resp.Code == "") {
		return nil, c.apiError("this app is not approved to keep the wallet unlocked", CodeNotApproved, status)
	}
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

// releaseKeyType is the key type of the wallet's release key.
const releaseKeyType = "ed25519"

// ReleaseKey returns the wallet's Orama release public key, the ed25519 key a
// TUF root lists for the roles this wallet signs (SignForPurpose with
// PurposeOramaRelease). Only the public key leaves the agent. A headless agent
// has no such key and answers 404.
func (c *Client) ReleaseKey(ctx context.Context) (ed25519.PublicKey, error) {
	var resp apiResponse[ReleaseKeyData]
	status, err := c.doJSON(ctx, "GET", "/v1/orama/release/key", nil, &resp)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, c.apiError(resp.Error, resp.Code, status)
	}
	if resp.Data.KeyType != releaseKeyType {
		return nil, fmt.Errorf("the agent's release key is %q, not %s", resp.Data.KeyType, releaseKeyType)
	}
	key, err := hex.DecodeString(strings.TrimPrefix(resp.Data.PublicKey, "0x"))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("the agent's release key is not %d bytes of hex", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(key), nil
}

// Sign signs a message with the wallet's private key.
// The desktop app may prompt the user for approval on first use.
func (c *Client) Sign(ctx context.Context, message, chain string) (*WalletSignData, error) {
	if purpose := ReservedPurpose(message); purpose != "" {
		return nil, fmt.Errorf("%w: a %s message is signed only under that purpose", ErrPurposeMismatch, purpose)
	}
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

// PurposeOramaRelease scopes a signature to a TUF release metadata payload:
// the canonical JSON of a "signed" section, which always opens with
// ReleasePayloadPrefix. The agent signs it only under this purpose, only for a
// caller granted wallet:sign:orama-release, and with the dedicated ed25519
// release key, never the account key an archive is signed with.
const PurposeOramaRelease = "orama-release"

// CapabilityOramaRelease is the agent grant PurposeOramaRelease needs. It is
// per signature and never granted to the headless agent.
const CapabilityOramaRelease = "wallet:sign:orama-release"

// ArchiveMessageHeader is line 1 of every build-archive signing message.
const ArchiveMessageHeader = "Orama build archive v1"

// ReleasePayloadPrefix opens every TUF "signed" section in canonical JSON:
// "_type" sorts before every other key, so the payload identifies itself.
const ReleasePayloadPrefix = `{"_type":"`

// ErrPurposeMismatch is a message that does not belong to the signing purpose
// it was sent under. It is raised before the agent is contacted, so an archive
// message is never offered as a release payload and a release payload is never
// offered as an archive message.
var ErrPurposeMismatch = errors.New("message does not match the signing purpose")

// ReservedPurpose reports which registered signing purpose message belongs to,
// or "" when it is in no registered format. The agent applies the same rule.
func ReservedPurpose(message string) string {
	switch {
	case strings.HasPrefix(message, ArchiveMessageHeader):
		return PurposeOramaArchive
	case strings.HasPrefix(message, ReleasePayloadPrefix):
		return PurposeOramaRelease
	}
	return ""
}

// checkPurpose refuses a message that is not in the format of the purpose it
// is sent under, and any purpose this client does not know.
func checkPurpose(message, purpose string) error {
	switch purpose {
	case PurposeOramaArchive, PurposeOramaRelease:
	default:
		return fmt.Errorf("%w: unknown purpose %q", ErrPurposeMismatch, purpose)
	}
	if got := ReservedPurpose(message); got != purpose {
		if got == "" {
			return fmt.Errorf("%w: the message is not in the %s format", ErrPurposeMismatch, purpose)
		}
		return fmt.Errorf("%w: the message is in the %s format, not %s", ErrPurposeMismatch, got, purpose)
	}
	return nil
}

// SignForPurpose signs message under a domain-separated purpose. The message
// must be in that purpose's format and no other: the archive purpose never
// signs a release payload and the release purpose never signs an archive.
// The agent checks the same and that the caller holds the purpose's own grant.
func (c *Client) SignForPurpose(ctx context.Context, message, chain, purpose string) (*WalletSignData, error) {
	if err := checkPurpose(message, purpose); err != nil {
		return nil, err
	}
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

// SignOramaTx asks the agent to sign one ORAMA transaction with
// SIGN_MODE_DIRECT. signDoc is the protobuf-encoded cosmos.tx.v1beta1.SignDoc,
// sent as-is: the agent decodes it itself, shows the decoded transaction in
// the RootWallet desktop app, and signs only if the user approves this one
// request. Every call prompts; approving never covers the next one.
//
// The answer is checked before it is returned: the signature must verify
// against the returned key over SHA-256 of signDoc.
func (c *Client) SignOramaTx(ctx context.Context, signDoc []byte) (*OramaTxSignature, error) {
	body := map[string]string{"signDoc": base64.StdEncoding.EncodeToString(signDoc)}

	var resp apiResponse[oramaTxSignData]
	status, err := c.doJSON(ctx, "POST", "/v1/orama/tx/sign", body, &resp)
	if err != nil {
		return nil, fmt.Errorf("sign orama tx: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("sign orama tx: %w", c.apiError(resp.Error, resp.Code, status))
	}
	sig, err := decodeOramaTxSignature(resp.Data, signDoc)
	if err != nil {
		return nil, fmt.Errorf("sign orama tx: %w", err)
	}
	return sig, nil
}

// errMalformedOramaTxSignature is an agent answer that is not a signature of
// the SignDoc it was sent.
var errMalformedOramaTxSignature = errors.New("the RootWallet agent answered with a malformed ORAMA signature")

const (
	oramaSignatureBytes = 64
	oramaPubKeyBytes    = 33
	oramaAddressPrefix  = "orama1"
)

func decodeOramaTxSignature(data oramaTxSignData, signDoc []byte) (*OramaTxSignature, error) {
	signature, err := base64.StdEncoding.DecodeString(data.Signature)
	if err != nil {
		return nil, fmt.Errorf("%w: signature: %w", errMalformedOramaTxSignature, err)
	}
	pubKey, err := base64.StdEncoding.DecodeString(data.PubKey)
	if err != nil {
		return nil, fmt.Errorf("%w: pubKey: %w", errMalformedOramaTxSignature, err)
	}
	if len(signature) != oramaSignatureBytes || len(pubKey) != oramaPubKeyBytes {
		return nil, fmt.Errorf("%w: signature is %d bytes and pubKey %d, want %d and %d",
			errMalformedOramaTxSignature, len(signature), len(pubKey), oramaSignatureBytes, oramaPubKeyBytes)
	}
	if !strings.HasPrefix(data.Address, oramaAddressPrefix) {
		return nil, fmt.Errorf("%w: address %q is not an ORAMA address", errMalformedOramaTxSignature, data.Address)
	}
	digest := sha256.Sum256(signDoc)
	if !crypto.VerifySignature(pubKey, digest[:], signature) {
		return nil, fmt.Errorf("%w: it does not verify against its key over this SignDoc", errMalformedOramaTxSignature)
	}
	return &OramaTxSignature{Signature: signature, PubKey: pubKey, Address: data.Address}, nil
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
	if c.guardErr != nil {
		return 0, c.guardErr
	}
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
// owned by the caller, and not group- or world-writable. Connecting to a
// Unix socket takes the write bit, so 0755 — what the agent creates — does
// not let another user connect. 0666 and 0660 do.
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
	if mode.Perm()&0o022 != 0 {
		return fmt.Errorf("rootwallet agent socket is group- or world-writable (mode %o)", mode.Perm())
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
