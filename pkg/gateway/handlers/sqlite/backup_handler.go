package sqlite

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
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
	namespace := ctx.Value("namespace").(string)

	var req struct {
		DatabaseName string `json:"database_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.DatabaseName == "" {
		http.Error(w, "database_name is required", http.StatusBadRequest)
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

	filePath := dbMeta["file_path"].(string)

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

	// Update backup metadata
	now := time.Now()
	query := `
		UPDATE namespace_sqlite_databases
		SET backup_cid = ?, last_backup_at = ?
		WHERE namespace = ? AND database_name = ?
	`

	_, err = h.sqliteHandler.db.Exec(ctx, query, cid, now, namespace, req.DatabaseName)
	if err != nil {
		h.logger.Error("Failed to update backup metadata", zap.Error(err))
		http.Error(w, "Failed to update backup metadata", http.StatusInternalServerError)
		return
	}

	// Record backup in history
	h.recordBackup(ctx, dbMeta["id"].(string), cid)

	h.logger.Info("Database backed up",
		zap.String("namespace", namespace),
		zap.String("database", req.DatabaseName),
		zap.String("cid", cid),
	)

	// Return response
	resp := map[string]interface{}{
		"database_name":  req.DatabaseName,
		"backup_cid":     cid,
		"backed_up_at":   now,
		"ipfs_url":       "https://ipfs.io/ipfs/" + cid,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// recordBackup records a backup in history
func (h *BackupHandler) recordBackup(ctx context.Context, dbID, cid string) {
	query := `
		INSERT INTO namespace_sqlite_backups (database_id, backup_cid, backed_up_at, size_bytes)
		SELECT id, ?, ?, size_bytes FROM namespace_sqlite_databases WHERE id = ?
	`

	_, err := h.sqliteHandler.db.Exec(ctx, query, cid, time.Now(), dbID)
	if err != nil {
		h.logger.Error("Failed to record backup", zap.Error(err))
	}
}

// ListBackups lists all backups for a database
func (h *BackupHandler) ListBackups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace := ctx.Value("namespace").(string)

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
		BackupCID   string    `db:"backup_cid"`
		BackedUpAt  time.Time `db:"backed_up_at"`
		SizeBytes   int64     `db:"size_bytes"`
	}

	var rows []backupRow
	query := `
		SELECT backup_cid, backed_up_at, size_bytes
		FROM namespace_sqlite_backups
		WHERE database_id = ?
		ORDER BY backed_up_at DESC
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
			"backed_up_at": row.BackedUpAt,
			"size_bytes":   row.SizeBytes,
			"ipfs_url":     "https://ipfs.io/ipfs/" + row.BackupCID,
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
