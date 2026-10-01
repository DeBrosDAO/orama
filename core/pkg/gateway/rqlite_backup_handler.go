package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	backuphandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/backup"
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

// rqliteImportMaxBytes is the largest database a namespace gateway imports: the
// largest a backup of the namespace would take, so what can be backed up can be
// loaded, and what is spooled and checked in SQLite. The cluster gateway's
// registry import, which an operator runs and which is streamed, has no cap, as
// its export has none.
const rqliteImportMaxBytes = nsbackup.MaxRQLiteBytes

const importTooLarge = "the import is over the largest database a namespace may load; nothing was imported"

// loadGuarder is the backup handler as an import sees it: it checks an image
// before it is loaded, reads what the load would replace and must put back, and
// returns what puts it back and scrubs the loaded database.
type loadGuarder interface {
	CheckImage(ctx context.Context, path string) error
	GuardLoad(ctx context.Context) (finish func(context.Context) error, err error)
}

// wholeDatabaseSlot is the one transfer this gateway runs at a time, shared by
// backup, restore, export and import.
func (g *Gateway) wholeDatabaseSlot() *backuphandlers.Slot {
	g.transferSlotOnce.Do(func() { g.transferSlot = backuphandlers.NewSlot() })
	return g.transferSlot
}

// importGuard is how this gateway guards a load: nil on the cluster gateway,
// whose database is the registry and whose import is the operator's whole, and
// the backup handler on a namespace gateway, which refuses the import when it
// has none.
func (g *Gateway) importGuard(w http.ResponseWriter) (loadGuarder, bool) {
	if g.servesCoreRegistry() {
		return nil, true
	}
	if g.loadGuard == nil {
		writeError(w, http.StatusServiceUnavailable, backupUnavailable)
		return nil, false
	}
	return g.loadGuard, true
}

// refuseWholeDatabaseToNonOwner refuses a namespace's whole-database export or
// import to anyone but its owner, and reports whether it did.
//
// A namespace's RQLite holds the tenant's own rows and the platform's rows
// about the namespace that its gateway reads there: functions and their
// secrets, stored-object ownership, quotas, push and WebRTC settings. (Keys,
// grants and sessions are in the cluster registry; the image still carries
// stale copies of them that nothing reads.) The SQL guard keeps statements off
// those rows, but a snapshot is every row and a load replaces every row, and
// no statement filter sees either. With db:read and db:write they were a
// developer's way to read every function secret and to write the rows the
// gateway trusts. The backup and restore routes already require the owner;
// these are the same act without the sealing. The cluster gateway's own registry is
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

// rqliteExportHandler handles GET /v1/rqlite/export
// Proxies to the namespace's RQLite /db/backup endpoint to download a raw SQLite snapshot.
// Protected by requiresNamespaceOwnership() via the /v1/rqlite/ prefix, and on a
// namespace gateway by refuseWholeDatabaseToNonOwner.
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
	release, ok := g.wholeDatabaseSlot().Begin(w)
	if !ok {
		return
	}
	defer release()
	if !extendTransferDeadlines(w, r) {
		return
	}

	backupURL := rqliteURL + rqliteBackupPath

	exportReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, backupURL, nil)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite export: failed to build the request",
			zap.String("error", rqlite.RedactError(err, backupURL)))
		writeError(w, http.StatusInternalServerError, "failed to create request")
		return
	}
	resp, err := newRQLiteSnapshotClient().Do(exportReq)
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
// Loads the request body (a raw SQLite database) into RQLite's /db/load.
// This is a DESTRUCTIVE operation that replaces the entire database.
// Protected by requiresNamespaceOwnership() via the /v1/rqlite/ prefix.
//
// On a namespace gateway the image is spooled to a file of its own, opened in
// SQLite and refused unless it is intact and carries nothing a namespace's
// database may not (backuphandlers.Handler.CheckImage) before RQLite is asked to
// load it: afterwards every gateway of the namespace serves writes against it.
// The load and the scrub that follows it run detached from the request, and the
// scrub runs after any outcome once the image was handed to RQLite.
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
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/octet-stream") {
		writeError(w, http.StatusBadRequest, "Content-Type must be application/octet-stream")
		return
	}
	guard, ok := g.importGuard(w)
	if !ok {
		return
	}
	if guard != nil && r.ContentLength > rqliteImportMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, importTooLarge)
		return
	}
	release, ok := g.wholeDatabaseSlot().Begin(w)
	if !ok {
		return
	}
	defer release()
	if !extendTransferDeadlines(w, r) {
		return
	}
	body, length, cleanup, ok := g.importSource(w, r, guard)
	if !ok {
		return
	}
	defer cleanup()

	ctx, cancel := backuphandlers.LoadContext(r)
	defer cancel()
	finish := func(context.Context) error { return nil }
	if guard != nil {
		var err error
		if finish, err = guard.GuardLoad(ctx); err != nil {
			g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: could not read what the load replaces",
				zap.Error(err))
			writeError(w, http.StatusBadGateway, "could not read the namespace's quota before the import; nothing was imported")
			return
		}
	}
	status, message := g.postLoad(ctx, rqliteURL+rqliteLoadPath, body, length)
	// The image was handed to RQLite: whatever it answered, or failed to, it
	// may have applied it.
	scrubErr := finish(ctx)
	g.renewTransferDeadlines(w)
	if err := scrubErr; err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: the loaded database could not be scrubbed", zap.Error(err))
		writeError(w, http.StatusBadGateway,
			"the database may have been imported but could not be checked; running the same import again is safe")
		return
	}
	if status != http.StatusOK {
		writeError(w, status, message)
		return
	}
	g.logger.ComponentInfo(logging.ComponentGeneral, "rqlite import completed successfully")
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "message": "database imported successfully"})
}

// importSource is the image to load, its length (-1 when unknown) and what
// removes it. On a namespace gateway (guard set) it is a spooled, checked file;
// on the cluster gateway, whose registry is an operator's to replace, the
// request body itself, which must begin as a SQLite database.
//
// RQLite's /db/load takes a SQL dump as well as a database file, and reads a
// body that is neither as an empty dump: 200, nothing loaded. A dump is
// statements run against the whole database, which an import of the database
// file is not.
func (g *Gateway) importSource(w http.ResponseWriter, r *http.Request, guard loadGuarder) (io.Reader, int64, func(), bool) {
	if guard == nil {
		body, ok := requireSQLiteImage(w, r.Body)
		return body, r.ContentLength, func() {}, ok
	}
	path, size, cleanup, err := backuphandlers.Spool(http.MaxBytesReader(w, r.Body, rqliteImportMaxBytes))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, importTooLarge)
		return nil, 0, nil, false
	case err != nil:
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: could not read the image", zap.Error(err))
		writeError(w, http.StatusBadRequest, "the import could not be read")
		return nil, 0, nil, false
	}
	if err := guard.CheckImage(r.Context(), path); err != nil {
		cleanup()
		status := http.StatusBadGateway
		if errors.Is(err, backuphandlers.ErrImageRefused) {
			status = http.StatusBadRequest
		} else {
			g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: could not check the image", zap.Error(err))
		}
		writeError(w, status, err.Error()+"; nothing was imported")
		return nil, 0, nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		cleanup()
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: could not reopen the checked image", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "the import could not be read back; nothing was imported")
		return nil, 0, nil, false
	}
	return f, size, func() { f.Close(); cleanup() }, true
}

// postLoad sends the image to RQLite and returns the status to answer with and,
// when it is not 200, the message.
func (g *Gateway) postLoad(ctx context.Context, loadURL string, body io.Reader, length int64) (int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loadURL, body)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: failed to build the proxy request",
			zap.String("error", rqlite.RedactError(err, loadURL)))
		return http.StatusInternalServerError, "failed to create proxy request"
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if length > 0 {
		req.ContentLength = length
	}
	resp, err := newRQLiteSnapshotClient().Do(req)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import: failed to reach RQLite load endpoint",
			zap.String("url", rqlite.RedactDSN(loadURL)), zap.String("error", rqlite.RedactError(err, loadURL)))
		return http.StatusBadGateway, "failed to reach RQLite"
	}
	defer resp.Body.Close()
	if isRedirect(resp.StatusCode) {
		g.logger.ComponentError(logging.ComponentGeneral, "rqlite import refused",
			zap.Error(rqliteStatusError(rqliteLoadPath, resp)))
		return http.StatusBadGateway, "RQLite redirected the load instead of applying it; nothing was imported"
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, rqliteErrorBodyBytes))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Sprintf("RQLite load failed: %s", string(respBody))
	}
	return http.StatusOK, ""
}

// requireSQLiteImage reads the first bytes of an import and answers 400 unless
// they are a SQLite file's header. It returns a reader that yields the whole
// body again, so a database is still streamed rather than held.
func requireSQLiteImage(w http.ResponseWriter, body io.Reader) (io.Reader, bool) {
	head := make([]byte, len(nsbackup.SQLiteMagic))
	n, err := io.ReadFull(body, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		writeError(w, http.StatusBadRequest, "the import could not be read")
		return nil, false
	}
	if !bytes.HasPrefix(head[:n], []byte(nsbackup.SQLiteMagic)) {
		writeError(w, http.StatusBadRequest, "the body is not a SQLite database file (as 'orama namespace rqlite export' writes); nothing was imported")
		return nil, false
	}
	return io.MultiReader(bytes.NewReader(head[:n]), body), true
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
