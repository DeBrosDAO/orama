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

// rqliteExportHandler handles GET /v1/rqlite/export
// Proxies to the namespace's RQLite /db/backup endpoint to download a raw SQLite snapshot.
// Protected by requiresNamespaceOwnership() via the /v1/rqlite/ prefix.
func (g *Gateway) rqliteExportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rqliteURL := g.rqliteBaseURL()
	if rqliteURL == "" {
		writeError(w, http.StatusServiceUnavailable, "RQLite not configured")
		return
	}

	backupURL := rqliteURL + rqliteBackupPath

	client := &http.Client{Timeout: rqliteSnapshotTimeout}
	resp, err := client.Get(backupURL)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite export: failed to reach RQLite backup endpoint",
			zap.String("url", backupURL), zap.Error(err))
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to reach RQLite: %v", err))
		return
	}
	defer resp.Body.Close()

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
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create proxy request: %v", err))
		return
	}
	proxyReq.Header.Set("Content-Type", "application/octet-stream")
	if r.ContentLength > 0 {
		proxyReq.ContentLength = r.ContentLength
	}

	client := &http.Client{Timeout: rqliteSnapshotTimeout}
	resp, err := client.Do(proxyReq)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: failed to reach RQLite load endpoint",
			zap.String("url", loadURL), zap.Error(err))
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to reach RQLite: %v", err))
		return
	}
	defer resp.Body.Close()

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
}

func newRQLiteSnapshots(baseURL string) rqliteSnapshots {
	return rqliteSnapshots{baseURL: baseURL, client: &http.Client{Timeout: rqliteSnapshotTimeout}}
}

// Backup returns the whole database as a SQLite file.
func (s rqliteSnapshots) Backup(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+rqliteBackupPath, nil)
	if err != nil {
		return nil, fmt.Errorf("build the RQLite backup request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach RQLite %s: %w", rqliteBackupPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, rqliteErrorBodyBytes))
		return nil, fmt.Errorf("RQLite %s returned %d: %s", rqliteBackupPath, resp.StatusCode, body)
	}
	db, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read the RQLite backup: %w", err)
	}
	return db, nil
}

// Load replaces the whole database with db.
func (s rqliteSnapshots) Load(ctx context.Context, db []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+rqliteLoadPath, bytes.NewReader(db))
	if err != nil {
		return fmt.Errorf("build the RQLite load request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("reach RQLite %s: %w", rqliteLoadPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, rqliteErrorBodyBytes))
		return fmt.Errorf("RQLite %s returned %d: %s", rqliteLoadPath, resp.StatusCode, body)
	}
	return nil
}
