package sqlite

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// BackupHandler handles database backups
type BackupHandler struct {
	sqliteHandler *SQLiteHandler
	ipfsClient    ipfs.IPFSClient
	logger        *zap.Logger
}

// NewBackupHandler creates a new backup handler
func NewBackupHandler(sqliteHandler *SQLiteHandler, ipfsClient ipfs.IPFSClient, logger *zap.Logger) *BackupHandler {
	return &BackupHandler{
		sqliteHandler: sqliteHandler,
		ipfsClient:    ipfsClient,
		logger:        logger,
	}
}

// BackupDatabase backs up a database to IPFS
func (h *BackupHandler) BackupDatabase(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace, ok := ctx.Value(ctxkeys.NamespaceOverride).(string)
	if !ok || namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	var req struct {
		DatabaseName string `json:"database_name"`
	}

	rawBody, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // 1MB
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(rawBody, &req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if !isValidDatabaseName(req.DatabaseName) {
		http.Error(w, "database_name must be 1-64 letters, digits, underscores or hyphens", http.StatusBadRequest)
		return
	}

	h.logger.Info("Backing up database",
		zap.String("namespace", namespace),
		zap.String("database", req.DatabaseName),
	)

	// Get database metadata
	dbMeta, err := h.sqliteHandler.getDatabaseRecord(ctx, namespace, req.DatabaseName)
	if err != nil {
		http.Error(w, "Database not found", http.StatusNotFound)
		return
	}

	// The file is on one disk. A backup that arrives elsewhere is run on that
	// node, the same way a query is: this machine does not have the bytes.
	homeNodeID, _ := dbMeta["home_node_id"].(string)
	if h.sqliteHandler.currentNodeID != "" && homeNodeID != "" && homeNodeID != h.sqliteHandler.currentNodeID {
		w.Header().Set("X-Orama-Home-Node", homeNodeID)
		if h.sqliteHandler.forwardToHome(w, r, rawBody, homeNodeID) {
			return
		}
		http.Error(w, "Database is on a different node and this gateway could not reach it", http.StatusMisdirectedRequest)
		return
	}

	filePath := h.sqliteHandler.databasePath(namespace, req.DatabaseName)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "Database file not found", http.StatusNotFound)
		return
	}

	// Open file for reading
	file, err := os.Open(filePath)
	if err != nil {
		h.logger.Error("Failed to open database file", zap.Error(err))
		http.Error(w, "Failed to open database file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	// Upload to IPFS
	addResp, err := h.ipfsClient.Add(ctx, file, req.DatabaseName+".db")
	if err != nil {
		h.logger.Error("Failed to upload to IPFS", zap.Error(err))
		http.Error(w, "Failed to backup database", http.StatusInternalServerError)
		return
	}

	cid := addResp.Cid

	info, err := file.Stat()
	if err != nil {
		h.logger.Error("Failed to stat database file", zap.Error(err))
		http.Error(w, "Failed to backup database", http.StatusInternalServerError)
		return
	}
	actor, ok := backupActor(r)
	if !ok {
		http.Error(w, "the caller has no identity to record on the backup", http.StatusUnauthorized)
		return
	}

	// History first, then the pointer at the latest one. The table is
	// migration 006: id, database_id, backup_cid, size_bytes, backup_type,
	// created_at, created_by. There is no backed_up_at column.
	now := time.Now().UTC()
	dbID, _ := dbMeta["id"].(string)
	if err := h.recordBackup(ctx, dbID, cid, info.Size(), actor, now); err != nil {
		h.logger.Error("Failed to record backup", zap.Error(err))
		http.Error(w, "Failed to record backup", http.StatusInternalServerError)
		return
	}

	query := `
		UPDATE namespace_sqlite_databases
		SET backup_cid = ?, last_backup_at = ?
		WHERE namespace = ? AND database_name = ?
	`
	if _, err = h.sqliteHandler.db.Exec(ctx, query, cid, now, namespace, req.DatabaseName); err != nil {
		h.logger.Error("Failed to update backup metadata", zap.Error(err))
		http.Error(w, "Failed to update backup metadata", http.StatusInternalServerError)
		return
	}

	h.logger.Info("Database backed up",
		zap.String("namespace", namespace),
		zap.String("database", req.DatabaseName),
		zap.String("cid", cid),
	)

	// Return response
	resp := map[string]interface{}{
		"database_name": req.DatabaseName,
		"backup_cid":    cid,
		"backed_up_at":  now,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// recordBackup records a backup in history.
//
// The columns are the ones migration 006 created. backed_up_at is the name of
// a column on namespace_sqlite_databases, not on this table, and writing it
// here is what made every backup history query fail.
func (h *BackupHandler) recordBackup(ctx context.Context, dbID, cid string, size int64, actor string, at time.Time) error {
	_, err := h.sqliteHandler.db.Exec(ctx, `
		INSERT INTO namespace_sqlite_backups
			(id, database_id, backup_cid, size_bytes, backup_type, created_at, created_by)
		VALUES (?, ?, ?, ?, 'manual', ?, ?)`,
		uuid.New().String(), dbID, cid, size, at.UTC().Format(time.RFC3339), actor)
	return err
}

// backupActor is who asked for the backup. A raw API key is not stored: the
// history would then hold a live credential.
func backupActor(r *http.Request) (string, bool) {
	if claims, ok := r.Context().Value(ctxkeys.JWT).(*auth.JWTClaims); ok {
		if subject := strings.TrimSpace(claims.Sub); subject != "" {
			return subject, true
		}
	}
	if key, ok := r.Context().Value(ctxkeys.APIKey).(string); ok && strings.TrimSpace(key) != "" {
		return "api key", true
	}
	return "", false
}

// ListBackups lists all backups for a database
func (h *BackupHandler) ListBackups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace, ok := ctx.Value(ctxkeys.NamespaceOverride).(string)
	if !ok || namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	databaseName := r.URL.Query().Get("database_name")
	if databaseName == "" {
		http.Error(w, "database_name query parameter is required", http.StatusBadRequest)
		return
	}

	// Get database ID
	dbMeta, err := h.sqliteHandler.getDatabaseRecord(ctx, namespace, databaseName)
	if err != nil {
		http.Error(w, "Database not found", http.StatusNotFound)
		return
	}

	dbID := dbMeta["id"].(string)

	// Query backups
	type backupRow struct {
		BackupCID string    `db:"backup_cid"`
		CreatedAt time.Time `db:"created_at"`
		SizeBytes int64     `db:"size_bytes"`
	}

	var rows []backupRow
	query := `
		SELECT backup_cid, created_at, size_bytes
		FROM namespace_sqlite_backups
		WHERE database_id = ?
		ORDER BY created_at DESC
		LIMIT 50
	`

	err = h.sqliteHandler.db.Query(ctx, &rows, query, dbID)
	if err != nil {
		h.logger.Error("Failed to query backups", zap.Error(err))
		http.Error(w, "Failed to query backups", http.StatusInternalServerError)
		return
	}

	backups := make([]map[string]interface{}, len(rows))
	for i, row := range rows {
		backups[i] = map[string]interface{}{
			"backup_cid":   row.BackupCID,
			"backed_up_at": row.CreatedAt,
			"size_bytes":   row.SizeBytes,
		}
	}

	resp := map[string]interface{}{
		"database_name": databaseName,
		"backups":       backups,
		"total":         len(backups),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
