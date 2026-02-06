package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/google/uuid"
	"go.uber.org/zap"
	_ "github.com/mattn/go-sqlite3"
)

// SQLiteHandler handles namespace SQLite database operations
type SQLiteHandler struct {
	db              rqlite.Client
	homeNodeManager *deployments.HomeNodeManager
	logger          *zap.Logger
	basePath        string
	currentNodeID   string // The node's peer ID for affinity checks
}

// NewSQLiteHandler creates a new SQLite handler
// dataDir: Base directory for node-local data (if empty, defaults to ~/.orama)
// nodeID: The node's peer ID for affinity checks (can be empty for single-node setups)
func NewSQLiteHandler(db rqlite.Client, homeNodeManager *deployments.HomeNodeManager, logger *zap.Logger, dataDir string, nodeID string) *SQLiteHandler {
	var basePath string

	if dataDir != "" {
		basePath = filepath.Join(dataDir, "sqlite")
	} else {
		// Use user's home directory for cross-platform compatibility
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Error("Failed to get user home directory", zap.Error(err))
			homeDir = os.Getenv("HOME")
		}
		basePath = filepath.Join(homeDir, ".orama", "sqlite")
	}

	return &SQLiteHandler{
		db:              db,
		homeNodeManager: homeNodeManager,
		logger:          logger,
		basePath:        basePath,
		currentNodeID:   nodeID,
	}
}

// writeCreateError writes an error response as JSON for consistency
func writeCreateError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// CreateDatabase creates a new SQLite database for a namespace
func (h *SQLiteHandler) CreateDatabase(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace, ok := ctx.Value(ctxkeys.NamespaceOverride).(string)
	if !ok || namespace == "" {
		writeCreateError(w, http.StatusUnauthorized, "Namespace not found in context")
		return
	}

	var req struct {
		DatabaseName string `json:"database_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCreateError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.DatabaseName == "" {
		writeCreateError(w, http.StatusBadRequest, "database_name is required")
		return
	}

	// Validate database name (alphanumeric, underscore, hyphen only)
	if !isValidDatabaseName(req.DatabaseName) {
		writeCreateError(w, http.StatusBadRequest, "Invalid database name. Use only alphanumeric characters, underscores, and hyphens")
		return
	}

	h.logger.Info("Creating SQLite database",
		zap.String("namespace", namespace),
		zap.String("database", req.DatabaseName),
	)

	// For SQLite databases, the home node is ALWAYS the current node
	// because the database file is stored locally on this node's filesystem.
	// This is different from deployments which can be load-balanced across nodes.
	homeNodeID := h.currentNodeID
	if homeNodeID == "" {
		// Fallback: if node ID not configured, try to get from HomeNodeManager
		// This provides backward compatibility for single-node setups
		var err error
		homeNodeID, err = h.homeNodeManager.AssignHomeNode(ctx, namespace)
		if err != nil {
			h.logger.Error("Failed to assign home node", zap.Error(err))
			writeCreateError(w, http.StatusInternalServerError, "Failed to assign home node")
			return
		}
	}

	// Check if database already exists
	existing, err := h.getDatabaseRecord(ctx, namespace, req.DatabaseName)
	if err == nil && existing != nil {
		writeCreateError(w, http.StatusConflict, "Database already exists")
		return
	}

	// Create database file path
	dbID := uuid.New().String()
	dbPath := filepath.Join(h.basePath, namespace, req.DatabaseName+".db")

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		h.logger.Error("Failed to create directory", zap.Error(err))
		writeCreateError(w, http.StatusInternalServerError, "Failed to create database directory")
		return
	}

	// Create SQLite database
	sqliteDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		h.logger.Error("Failed to create SQLite database", zap.Error(err))
		writeCreateError(w, http.StatusInternalServerError, "Failed to create database")
		return
	}

	// Enable WAL mode for better concurrency
	if _, err := sqliteDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		h.logger.Warn("Failed to enable WAL mode", zap.Error(err))
	}

	sqliteDB.Close()

	// Record in RQLite
	query := `
		INSERT INTO namespace_sqlite_databases (
			id, namespace, database_name, home_node_id, file_path, size_bytes, created_at, updated_at, created_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	now := time.Now()
	_, err = h.db.Exec(ctx, query, dbID, namespace, req.DatabaseName, homeNodeID, dbPath, 0, now, now, namespace)
	if err != nil {
		h.logger.Error("Failed to record database", zap.Error(err))
		os.Remove(dbPath) // Cleanup
		writeCreateError(w, http.StatusInternalServerError, "Failed to record database")
		return
	}

	h.logger.Info("SQLite database created",
		zap.String("id", dbID),
		zap.String("namespace", namespace),
		zap.String("database", req.DatabaseName),
		zap.String("path", dbPath),
	)

	// Return response
	resp := map[string]interface{}{
		"id":            dbID,
		"namespace":     namespace,
		"database_name": req.DatabaseName,
		"home_node_id":  homeNodeID,
		"file_path":     dbPath,
		"created_at":    now,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// getDatabaseRecord retrieves database metadata from RQLite
func (h *SQLiteHandler) getDatabaseRecord(ctx context.Context, namespace, databaseName string) (map[string]interface{}, error) {
	type dbRow struct {
		ID           string    `db:"id"`
		Namespace    string    `db:"namespace"`
		DatabaseName string    `db:"database_name"`
		HomeNodeID   string    `db:"home_node_id"`
		FilePath     string    `db:"file_path"`
		SizeBytes    int64     `db:"size_bytes"`
		BackupCID    string    `db:"backup_cid"`
		CreatedAt    time.Time `db:"created_at"`
	}

	var rows []dbRow
	query := `SELECT * FROM namespace_sqlite_databases WHERE namespace = ? AND database_name = ? LIMIT 1`
	err := h.db.Query(ctx, &rows, query, namespace, databaseName)
	if err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, fmt.Errorf("database not found")
	}

	row := rows[0]
	return map[string]interface{}{
		"id":            row.ID,
		"namespace":     row.Namespace,
		"database_name": row.DatabaseName,
		"home_node_id":  row.HomeNodeID,
		"file_path":     row.FilePath,
		"size_bytes":    row.SizeBytes,
		"backup_cid":    row.BackupCID,
		"created_at":    row.CreatedAt,
	}, nil
}

// isValidDatabaseName validates database name
func isValidDatabaseName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}

	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' || ch == '-') {
			return false
		}
	}

	return true
}
