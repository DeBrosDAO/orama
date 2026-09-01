package gateway

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
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

	backupURL := rqliteURL + "/db/backup"

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(backupURL)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite export: failed to reach RQLite backup endpoint",
			zap.String("url", backupURL), zap.Error(err))
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to reach RQLite: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
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

	loadURL := rqliteURL + "/db/load"

	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, loadURL, r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create proxy request: %v", err))
		return
	}
	proxyReq.Header.Set("Content-Type", "application/octet-stream")
	if r.ContentLength > 0 {
		proxyReq.ContentLength = r.ContentLength
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(proxyReq)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: failed to reach RQLite load endpoint",
			zap.String("url", loadURL), zap.Error(err))
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to reach RQLite: %v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

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
func (g *Gateway) rqliteBaseURL() string {
	dsn := g.cfg.RQLiteDSN
	if dsn == "" {
		dsn = "http://localhost:10100"
	}
	if idx := strings.Index(dsn, "?"); idx != -1 {
		dsn = dsn[:idx]
	}
	return strings.TrimRight(dsn, "/")
}
