package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"go.uber.org/zap"
)

// QueryRequest represents a SQL query request
type QueryRequest struct {
	DatabaseName string        `json:"database_name"`
	Query        string        `json:"query"`
	Params       []interface{} `json:"params"`
}

// QueryResponse represents a SQL query response
type QueryResponse struct {
	Columns []string        `json:"columns,omitempty"`
	Rows    [][]interface{} `json:"rows,omitempty"`
	RowsAffected int64       `json:"rows_affected,omitempty"`
	LastInsertID int64       `json:"last_insert_id,omitempty"`
	Error        string      `json:"error,omitempty"`
}

// QueryDatabase executes a SQL query on a namespace database
func (h *SQLiteHandler) QueryDatabase(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace, ok := ctx.Value(ctxkeys.NamespaceOverride).(string)
	if !ok || namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.DatabaseName == "" {
		http.Error(w, "database_name is required", http.StatusBadRequest)
		return
	}

	if req.Query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}

	// Get database metadata
	dbMeta, err := h.getDatabaseRecord(ctx, namespace, req.DatabaseName)
	if err != nil {
		http.Error(w, "Database not found", http.StatusNotFound)
		return
	}

	filePath := dbMeta["file_path"].(string)

	// Check if database file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "Database file not found", http.StatusNotFound)
		return
	}

	// Open database
	db, err := sql.Open("sqlite3", filePath)
	if err != nil {
		h.logger.Error("Failed to open database", zap.Error(err))
		http.Error(w, "Failed to open database", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Determine if this is a read or write query
	isWrite := isWriteQuery(req.Query)

	var resp QueryResponse

	if isWrite {
		// Execute write query
		result, err := db.ExecContext(ctx, req.Query, req.Params...)
		if err != nil {
			resp.Error = err.Error()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(resp)
			return
		}

		rowsAffected, _ := result.RowsAffected()
		lastInsertID, _ := result.LastInsertId()

		resp.RowsAffected = rowsAffected
		resp.LastInsertID = lastInsertID
	} else {
		// Execute read query
		rows, err := db.QueryContext(ctx, req.Query, req.Params...)
		if err != nil {
			resp.Error = err.Error()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(resp)
			return
		}
		defer rows.Close()

		// Get column names
		columns, err := rows.Columns()
		if err != nil {
			resp.Error = err.Error()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(resp)
			return
		}

		resp.Columns = columns

		// Scan rows
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		for rows.Next() {
			if err := rows.Scan(valuePtrs...); err != nil {
				h.logger.Error("Failed to scan row", zap.Error(err))
				continue
			}

			row := make([]interface{}, len(columns))
			for i, val := range values {
				// Convert []byte to string for JSON serialization
				if b, ok := val.([]byte); ok {
					row[i] = string(b)
				} else {
					row[i] = val
				}
			}

			resp.Rows = append(resp.Rows, row)
		}

		if err := rows.Err(); err != nil {
			resp.Error = err.Error()
		}
	}

	// Update database size
	go h.updateDatabaseSize(namespace, req.DatabaseName, filePath)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// isWriteQuery determines if a SQL query is a write operation
func isWriteQuery(query string) bool {
	upperQuery := strings.ToUpper(strings.TrimSpace(query))
	writeKeywords := []string{
		"INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER", "TRUNCATE", "REPLACE",
	}

	for _, keyword := range writeKeywords {
		if strings.HasPrefix(upperQuery, keyword) {
			return true
		}
	}

	return false
}

// updateDatabaseSize updates the size of the database in metadata
func (h *SQLiteHandler) updateDatabaseSize(namespace, databaseName, filePath string) {
	stat, err := os.Stat(filePath)
	if err != nil {
		h.logger.Error("Failed to stat database file", zap.Error(err))
		return
	}

	query := `UPDATE namespace_sqlite_databases SET size_bytes = ? WHERE namespace = ? AND database_name = ?`
	ctx := context.Background()
	_, err = h.db.Exec(ctx, query, stat.Size(), namespace, databaseName)
	if err != nil {
		h.logger.Error("Failed to update database size", zap.Error(err))
	}
}

// DatabaseInfo represents database metadata
type DatabaseInfo struct {
	ID           string `json:"id" db:"id"`
	DatabaseName string `json:"database_name" db:"database_name"`
	HomeNodeID   string `json:"home_node_id" db:"home_node_id"`
	SizeBytes    int64  `json:"size_bytes" db:"size_bytes"`
	BackupCID    string `json:"backup_cid,omitempty" db:"backup_cid"`
	LastBackupAt string `json:"last_backup_at,omitempty" db:"last_backup_at"`
	CreatedAt    string `json:"created_at" db:"created_at"`
}

// ListDatabases lists all databases for a namespace
func (h *SQLiteHandler) ListDatabases(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	namespace, ok := ctx.Value(ctxkeys.NamespaceOverride).(string)
	if !ok || namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	var databases []DatabaseInfo
	query := `
		SELECT id, database_name, home_node_id, size_bytes, backup_cid, last_backup_at, created_at
		FROM namespace_sqlite_databases
		WHERE namespace = ?
		ORDER BY created_at DESC
	`

	err := h.db.Query(ctx, &databases, query, namespace)
	if err != nil {
		h.logger.Error("Failed to list databases", zap.Error(err))
		http.Error(w, "Failed to list databases", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"databases": databases,
		"count":     len(databases),
	})
}
