package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// RQLite's native endpoints for a whole-database snapshot and its restore.
// /v1/rqlite/export, /v1/rqlite/import and the namespace backup all use them.
const (
	rqliteBackupPath = "/db/backup"
	rqliteLoadPath   = "/db/load"
	// rqliteSnapshotTimeout covers streaming the whole database either way.
	rqliteSnapshotTimeout = 5 * time.Minute
	// rqliteErrorBodyBytes is how much of an RQLite error body is kept.
	rqliteErrorBodyBytes = 4096
)

// newRQLiteSnapshotClient never follows a redirect. Go re-sends a redirected
// POST as a GET without its body, so a followed /db/load would load nothing.
// rqlite 8 forwards /db/load and /db/backup from a follower to the leader
// itself unless ?redirect is set, so a 3xx is a misconfiguration to report.
func newRQLiteSnapshotClient() *http.Client {
	return &http.Client{
		Timeout:       rqliteSnapshotTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// isRedirect reports whether RQLite answered with a redirect.
func isRedirect(status int) bool {
	return status >= http.StatusMultipleChoices && status < http.StatusBadRequest
}

// rqliteStatusError describes a non-200 answer from one of RQLite's native
// endpoints. It never carries the DSN's password.
func rqliteStatusError(path string, resp *http.Response) error {
	if isRedirect(resp.StatusCode) {
		return fmt.Errorf("RQLite %s answered %d, a redirect to %s; it is not followed, because a redirected POST loses its body",
			path, resp.StatusCode, rqlite.RedactDSN(resp.Header.Get("Location")))
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, rqliteErrorBodyBytes))
	return fmt.Errorf("RQLite %s returned %d: %s", path, resp.StatusCode, body)
}

// rqliteExportHandler handles GET /v1/rqlite/export
// Proxies to the namespace's RQLite /db/backup endpoint to download a raw SQLite snapshot.
// Protected by requiresNamespaceOwnership() via the /v1/rqlite/ prefix.
// refuseWholeDatabaseToNonOwner refuses a namespace's whole-database export or
// import to anyone but its owner, and reports whether it did.
//
// A namespace's RQLite holds the platform's rows about it as well as the
// tenant's own: keys, grants, secrets. The SQL guard keeps statements off
// them, but a snapshot is every row and a load replaces every row, and no
// statement filter sees either. With db:read and db:write they were a
// developer's way to read every credential and to write themselves an owner
// grant. The backup and restore routes already require the owner; these are
// the same act without the sealing. The cluster gateway's own registry is
// operator-only, and requireOperatorForCoreRegistry has refused everyone else.
func (g *Gateway) refuseWholeDatabaseToNonOwner(w http.ResponseWriter, r *http.Request) bool {
	if g.servesCoreRegistry() {
		return false
	}
	if _, owner := backupCaller(r); owner {
		return false
	}
	forbidden(w, CodeOwnershipRequired,
		"only the namespace's owner may export or import its whole database: it holds every key, grant and secret of the namespace", nil)
	return true
}

func (g *Gateway) rqliteExportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if g.refuseWholeDatabaseToNonOwner(w, r) {
		return
	}

	rqliteURL := g.rqliteBaseURL()
	if rqliteURL == "" {
		writeError(w, http.StatusServiceUnavailable, "RQLite not configured")
		return
	}

	backupURL := rqliteURL + rqliteBackupPath

	resp, err := newRQLiteSnapshotClient().Get(backupURL)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite export: failed to reach RQLite backup endpoint",
			zap.String("url", rqlite.RedactDSN(backupURL)), zap.String("error", rqlite.RedactError(err, backupURL)))
		writeError(w, http.StatusBadGateway, "failed to reach RQLite")
		return
	}
	defer resp.Body.Close()

	if isRedirect(resp.StatusCode) {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite export refused",
			zap.Error(rqliteStatusError(rqliteBackupPath, resp)))
		writeError(w, http.StatusBadGateway, "RQLite redirected the backup instead of serving it")
		return
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, rqliteErrorBodyBytes))
		writeError(w, resp.StatusCode, fmt.Sprintf("RQLite backup failed: %s", string(body)))
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=rqlite-export.db")
	if resp.ContentLength > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", resp.ContentLength))
	}
	w.WriteHeader(http.StatusOK)

	written, err := io.Copy(w, resp.Body)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite export: error streaming backup",
			zap.Int64("bytes_written", written), zap.Error(err))
		return
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "rqlite export completed", zap.Int64("bytes", written))
}

// rqliteImportHandler handles POST /v1/rqlite/import
// Proxies the request body (raw SQLite binary) to the namespace's RQLite /db/load endpoint.
// This is a DESTRUCTIVE operation that replaces the entire database.
// Protected by requiresNamespaceOwnership() via the /v1/rqlite/ prefix.
func (g *Gateway) rqliteImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if g.refuseWholeDatabaseToNonOwner(w, r) {
		return
	}

	rqliteURL := g.rqliteBaseURL()
	if rqliteURL == "" {
		writeError(w, http.StatusServiceUnavailable, "RQLite not configured")
		return
	}

	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/octet-stream") {
		writeError(w, http.StatusBadRequest, "Content-Type must be application/octet-stream")
		return
	}

	loadURL := rqliteURL + rqliteLoadPath

	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, loadURL, r.Body)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: failed to build the proxy request",
			zap.String("error", rqlite.RedactError(err, loadURL)))
		writeError(w, http.StatusInternalServerError, "failed to create proxy request")
		return
	}
	proxyReq.Header.Set("Content-Type", "application/octet-stream")
	if r.ContentLength > 0 {
		proxyReq.ContentLength = r.ContentLength
	}

	resp, err := newRQLiteSnapshotClient().Do(proxyReq)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: failed to reach RQLite load endpoint",
			zap.String("url", rqlite.RedactDSN(loadURL)), zap.String("error", rqlite.RedactError(err, loadURL)))
		writeError(w, http.StatusBadGateway, "failed to reach RQLite")
		return
	}
	defer resp.Body.Close()

	if isRedirect(resp.StatusCode) {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import refused",
			zap.Error(rqliteStatusError(rqliteLoadPath, resp)))
		writeError(w, http.StatusBadGateway, "RQLite redirected the load instead of applying it; nothing was imported")
		return
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, rqliteErrorBodyBytes))

	if resp.StatusCode != http.StatusOK {
		writeError(w, resp.StatusCode, fmt.Sprintf("RQLite load failed: %s", string(body)))
		return
	}

	g.logger.ComponentInfo(logging.ComponentGeneral, "rqlite import completed successfully")

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"message": "database imported successfully",
	})
}

// rqliteBaseURL returns the raw RQLite HTTP URL for proxying native API calls.
// rqlite_dsn is required by ValidateConfig and carries the credentials.
func (g *Gateway) rqliteBaseURL() string {
	dsn := g.cfg.RQLiteDSN
	if idx := strings.Index(dsn, "?"); idx != -1 {
		dsn = dsn[:idx]
	}
	return strings.TrimRight(dsn, "/")
}

// rqliteSnapshots is the namespace backup's handle on the same /db/backup and
// /db/load the export and import routes proxy.
type rqliteSnapshots struct {
	baseURL string
	client  *http.Client
	// maxBytes is the largest database Backup reads before refusing.
	maxBytes int64
}

func newRQLiteSnapshots(baseURL string) rqliteSnapshots {
	return rqliteSnapshots{baseURL: baseURL, client: newRQLiteSnapshotClient(), maxBytes: nsbackup.MaxRQLiteBytes}
}

// Backup returns the whole database as a SQLite file, or an error wrapping
// nsbackup.ErrTooLarge without reading past maxBytes.
func (s rqliteSnapshots) Backup(ctx context.Context) ([]byte, error) {
	url := s.baseURL + rqliteBackupPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build the RQLite backup request: %s", rqlite.RedactError(err, url))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach RQLite %s: %s", rqliteBackupPath, rqlite.RedactError(err, url))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, rqliteStatusError(rqliteBackupPath, resp)
	}
	db, err := io.ReadAll(io.LimitReader(resp.Body, s.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read the RQLite backup: %w", err)
	}
	if int64(len(db)) > s.maxBytes {
		return nil, fmt.Errorf("%w: the namespace database is over %d bytes", nsbackup.ErrTooLarge, s.maxBytes)
	}
	return db, nil
}

// Load replaces the whole database with db.
func (s rqliteSnapshots) Load(ctx context.Context, db []byte) error {
	url := s.baseURL + rqliteLoadPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(db))
	if err != nil {
		return fmt.Errorf("build the RQLite load request: %s", rqlite.RedactError(err, url))
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("reach RQLite %s: %s", rqliteLoadPath, rqlite.RedactError(err, url))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rqliteStatusError(rqliteLoadPath, resp)
	}
	return nil
}
