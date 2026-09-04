package sqlite

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"go.uber.org/zap"
)

// DeleteDatabase removes a namespace SQLite database and its file.
// POST /v1/db/sqlite/delete {"database_name": "..."}
//
// There was no way to remove a database. A name that was created by mistake,
// or a database an app no longer uses, stayed listed and kept its file on the
// node's disk for good.
//
// The record is removed before the files. A row without a file is a clear "not
// found" on the next call and can be recreated; a file without a row is
// invisible to every command and occupies disk nobody can account for.
func (h *SQLiteHandler) DeleteDatabase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "POST, DELETE")
		writeCreateError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx := r.Context()
	namespace, ok := ctx.Value(ctxkeys.NamespaceOverride).(string)
	if !ok || namespace == "" {
		writeCreateError(w, http.StatusUnauthorized, "Namespace not found in context")
		return
	}

	var req struct {
		DatabaseName string `json:"database_name"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCreateError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.DatabaseName == "" {
		writeCreateError(w, http.StatusBadRequest, "database_name is required")
		return
	}

	dbMeta, err := h.getDatabaseRecord(ctx, namespace, req.DatabaseName)
	if err != nil {
		writeCreateError(w, http.StatusNotFound, "Database not found")
		return
	}

	// A SQLite database is a file on one node's disk, so the delete has to
	// happen there. Answering anywhere else would drop the row and leave the
	// file behind, which is the one outcome this endpoint must not produce.
	homeNodeID, _ := dbMeta["home_node_id"].(string)
	if h.currentNodeID != "" && homeNodeID != "" && homeNodeID != h.currentNodeID {
		w.Header().Set("X-Orama-Home-Node", homeNodeID)
		h.logger.Warn("Database delete hit wrong node",
			zap.String("database", req.DatabaseName),
			zap.String("home_node", homeNodeID),
			zap.String("current_node", h.currentNodeID),
		)
		writeCreateError(w, http.StatusMisdirectedRequest, "Database is on a different node")
		return
	}

	filePath, _ := dbMeta["file_path"].(string)

	h.logger.Info("Deleting SQLite database",
		zap.String("namespace", namespace),
		zap.String("database", req.DatabaseName),
		zap.String("path", filePath),
	)

	_, err = h.db.Exec(ctx,
		`DELETE FROM namespace_sqlite_databases WHERE namespace = ? AND database_name = ?`,
		namespace, req.DatabaseName)
	if err != nil {
		h.logger.Error("Failed to delete database record", zap.Error(err))
		writeCreateError(w, http.StatusInternalServerError, "Failed to delete database record")
		return
	}

	removed, err := removeSQLiteFiles(filePath)
	if err != nil {
		// The record is already gone, so the database is deleted as far as
		// every command is concerned. Say what is left behind rather than
		// reporting a failure the caller cannot act on by retrying.
		h.logger.Error("Failed to remove database files",
			zap.String("path", filePath), zap.Error(err))
		writeCreateError(w, http.StatusInternalServerError,
			"Database record removed but its files could not be deleted: "+err.Error())
		return
	}

	h.logger.Info("SQLite database deleted",
		zap.String("namespace", namespace),
		zap.String("database", req.DatabaseName),
		zap.Strings("files_removed", removed),
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"database_name": req.DatabaseName,
		"namespace":     namespace,
		"files_removed": removed,
	})
}

// sqliteSidecarSuffixes are the files WAL mode leaves beside the database.
//
// Removing only the .db file leaves a -wal holding committed pages and a -shm
// holding the shared index, so a database created again under the same name
// would open on top of a previous tenant's uncheckpointed writes.
var sqliteSidecarSuffixes = []string{"", "-wal", "-shm", "-journal"}

// removeSQLiteFiles deletes the database file and its sidecars, returning the
// paths that existed. A file that is already gone is not an error: the delete
// has to be repeatable after a partial failure.
func removeSQLiteFiles(filePath string) ([]string, error) {
	if filePath == "" {
		return nil, nil
	}

	var removed []string
	var failures []string
	for _, suffix := range sqliteSidecarSuffixes {
		path := filePath + suffix
		err := os.Remove(path)
		switch {
		case err == nil:
			removed = append(removed, path)
		case os.IsNotExist(err):
			continue
		default:
			failures = append(failures, path+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		return removed, &removeError{failures: failures}
	}
	return removed, nil
}

// removeError names every file that could not be removed, rather than only the
// first: an operator fixing permissions needs the whole list.
type removeError struct {
	failures []string
}

func (e *removeError) Error() string {
	return strings.Join(e.failures, "; ")
}
