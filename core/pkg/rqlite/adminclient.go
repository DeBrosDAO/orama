package rqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	"go.uber.org/zap"
)

// The one HTTP client for rqlite's admin surface.
//
// Fourteen bare http.Client calls reached /status, /nodes, /join, /remove,
// /db/backup and transfer-leadership, none of them sending credentials, each
// with its own timeout. Today that works because rqlited is started without
// -auth — port 10100 is open on the mesh with no authentication at all. The
// moment auth is enabled, every one of them starts returning 401 and the node
// looks like raft has broken: reconciliation stops, backups stop, leadership
// transfer stops, and the logs say nothing about credentials.
//
// The sql.DB path already embeds credentials in its DSN. This is the same for
// the admin endpoints, in one place, so enabling auth is a configuration change
// rather than a rewrite.

// AdminClient talks to one rqlite node's admin API.
type AdminClient struct {
	baseURL string
	user    string
	pass    string
	http    *http.Client
}

// Admin request budgets. Grouped so there is one policy rather than fourteen.
const (
	// adminQuickTimeout covers reads that answer from memory: /status, /nodes.
	adminQuickTimeout = 5 * time.Second

	// adminChangeTimeout covers configuration changes, which are raft writes:
	// /join, /remove, transfer-leadership.
	adminChangeTimeout = 30 * time.Second

	// adminBackupTimeout covers /db/backup, which streams the whole database.
	adminBackupTimeout = 2 * time.Minute
)

// NewAdminClient builds a client for the rqlite at baseURL ("http://host:port").
//
// Empty credentials mean no Authorization header, which is correct while
// rqlited runs without -auth.
func NewAdminClient(baseURL, user, pass string) *AdminClient {
	return &AdminClient{
		baseURL: baseURL,
		user:    user,
		pass:    pass,
		// No client-level Timeout: each call sets its own deadline on the
		// context, and a fixed one here would be the tighter of the two. A
		// 5s client would cut /db/backup off five seconds into a snapshot
		// that is allowed two minutes, and the failure would look like the
		// database was unreachable.
		http: tlsutil.NewHTTPClient(0),
	}
}

// LocalAdminClient builds a client for this node's own rqlite, reading
// credentials from the auth file when one is configured.
func (r *RQLiteManager) LocalAdminClient() *AdminClient {
	user, pass := r.adminCredentials()
	return NewAdminClient(fmt.Sprintf("http://localhost:%d", r.config.RQLitePort), user, pass)
}

// adminCredentials reads the admin user out of the configured auth file.
//
// A file that exists but cannot be read is reported as no credentials rather
// than failing the caller: the request then gets a clear 401 from rqlite, which
// is a better diagnostic than a start-up failure with a file path in it.
func (r *RQLiteManager) adminCredentials() (user, pass string) {
	if r.config == nil || r.config.RQLiteAuthFile == "" {
		return "", ""
	}
	u, p, err := readRQLiteAuthFile(r.config.RQLiteAuthFile)
	if err != nil {
		r.logger.Warn("Cannot read the rqlite auth file; admin requests will be sent unauthenticated",
			zap.String("path", r.config.RQLiteAuthFile), zap.Error(err))
		return "", ""
	}
	return u, p
}

// readRQLiteAuthFile returns the first user in rqlite's auth JSON.
func readRQLiteAuthFile(path string) (user, pass string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read rqlite auth file: %w", err)
	}

	var entries []struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return "", "", fmt.Errorf("parse rqlite auth file: %w", err)
	}
	for _, e := range entries {
		if e.Username != "" {
			return e.Username, e.Password, nil
		}
	}
	return "", "", fmt.Errorf("rqlite auth file names no user")
}

// do performs one admin request with the client's credentials.
func (c *AdminClient) do(ctx context.Context, method, path string, body []byte, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The whole point: credentials on every admin call, in one place.
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	payload, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		// Named explicitly, because the symptom otherwise reads as "raft is
		// broken" and sends an operator looking in entirely the wrong place.
		return nil, fmt.Errorf("%s %s: rqlite rejected the credentials (401). "+
			"Either -auth is enabled and this caller has none, or the auth file has changed", method, path)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s returned %d: %s", method, path, resp.StatusCode, string(payload))
	}
	if readErr != nil {
		return nil, fmt.Errorf("read %s %s response: %w", method, path, readErr)
	}
	return payload, nil
}

// Status returns the node's /status.
func (c *AdminClient) Status(ctx context.Context) (*RQLiteStatus, error) {
	body, err := c.do(ctx, http.MethodGet, "/status", nil, adminQuickTimeout)
	if err != nil {
		return nil, err
	}
	var status RQLiteStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("decode status: %w", err)
	}
	return &status, nil
}

// Nodes returns the raft configuration.
func (c *AdminClient) Nodes(ctx context.Context) (RQLiteNodes, error) {
	body, err := c.do(ctx, http.MethodGet, "/nodes?nonvoters&ver=2&timeout=5s", nil, adminQuickTimeout)
	if err != nil {
		return nil, err
	}
	return decodeNodes(body)
}

// Join adds or moves a member. id and addr are separate values: the id is
// identity, the address is where to reach it.
func (c *AdminClient) Join(ctx context.Context, id, addr string, voter bool) error {
	payload, err := json.Marshal(map[string]any{"id": id, "addr": addr, "voter": voter})
	if err != nil {
		return fmt.Errorf("encode join: %w", err)
	}
	_, err = c.do(ctx, http.MethodPost, "/join", payload, adminChangeTimeout)
	return err
}

// Remove takes a member out of the raft configuration, by id.
func (c *AdminClient) Remove(ctx context.Context, id string) error {
	payload, err := json.Marshal(map[string]string{"id": id})
	if err != nil {
		return fmt.Errorf("encode remove: %w", err)
	}
	_, err = c.do(ctx, http.MethodDelete, "/remove", payload, adminChangeTimeout)
	return err
}

// Backup streams a consistent SQLite snapshot.
func (c *AdminClient) Backup(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodGet, "/db/backup", nil, adminBackupTimeout)
}

// TransferLeadership asks this node to hand leadership to id.
func (c *AdminClient) TransferLeadership(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodPost, "/leader?id="+id, nil, adminChangeTimeout)
	return err
}

// Ready reports whether the node's /readyz answers.
func (c *AdminClient) Ready(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodGet, "/readyz", nil, adminQuickTimeout)
	return err
}

// adminCredentialsFromFile reads an rqlite auth file, returning empty
// credentials when there is no file or it cannot be read.
//
// An unreadable file yields an unauthenticated request and therefore a clear
// 401 from rqlite, which tells an operator more than a start-up failure would.
func adminCredentialsFromFile(path string) (user, pass string) {
	if path == "" {
		return "", ""
	}
	u, p, err := readRQLiteAuthFile(path)
	if err != nil {
		return "", ""
	}
	return u, p
}
