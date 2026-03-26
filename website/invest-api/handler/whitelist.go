package handler

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/debros/orama-website/invest-api/auth"
)

func WhitelistJoinHandler(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wallet, chain, ok := auth.WalletFromContext(r.Context())
		if !ok {
			jsonError(w, http.StatusUnauthorized, "wallet required")
			return
		}

		var existing int
		database.QueryRow("SELECT COUNT(*) FROM whitelist WHERE wallet = ?", wallet).Scan(&existing)
		if existing > 0 {
			jsonError(w, http.StatusConflict, "wallet already on whitelist")
			return
		}

		_, err := database.Exec("INSERT INTO whitelist (wallet, chain) VALUES (?, ?)", wallet, chain)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to join whitelist")
			return
		}

		var position int
		database.QueryRow("SELECT COUNT(*) FROM whitelist").Scan(&position)

		logActivity(database, "whitelist_join", wallet, fmt.Sprintf("Position #%d", position))

		jsonResponse(w, http.StatusCreated, map[string]any{
			"position": position,
		})
	}
}
