package handler

import (
	"database/sql"
	"net/http"

	"github.com/debros/orama-website/invest-api/db"
)

func ActivityHandler(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := database.Query(
			"SELECT event_type, wallet, COALESCE(detail, ''), created_at FROM activity_log ORDER BY created_at DESC LIMIT 50",
		)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to query activity log")
			return
		}
		defer rows.Close()

		entries := []db.ActivityEntry{}
		for rows.Next() {
			var e db.ActivityEntry
			rows.Scan(&e.EventType, &e.Wallet, &e.Detail, &e.CreatedAt)
			entries = append(entries, e)
		}

		jsonResponse(w, http.StatusOK, entries)
	}
}
