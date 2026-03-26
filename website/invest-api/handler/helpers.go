package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonResponse(w, status, map[string]string{"error": msg})
}

func logActivity(database *sql.DB, eventType, wallet, detail string) {
	truncated := wallet
	if len(wallet) > 10 {
		truncated = wallet[:6] + "..." + wallet[len(wallet)-4:]
	}
	database.Exec(
		"INSERT INTO activity_log (event_type, wallet, detail) VALUES (?, ?, ?)",
		eventType, truncated, detail,
	)
}
