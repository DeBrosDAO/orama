package handler

import (
	"database/sql"
	"net/http"

	"github.com/debros/orama-website/invest-api/db"
)

func StatsHandler(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var stats db.StatsResponse

		database.QueryRow("SELECT COALESCE(SUM(tokens_allocated), 0), COALESCE(SUM(amount_paid), 0) FROM token_purchases").
			Scan(&stats.TokensSold, &stats.TokenRaised)

		database.QueryRow("SELECT COUNT(*), COALESCE(SUM(amount_paid), 0) FROM license_purchases").
			Scan(&stats.LicensesSold, &stats.LicenseRaised)

		database.QueryRow("SELECT COUNT(*) FROM whitelist").Scan(&stats.WhitelistCount)

		stats.TokensRemaining = db.TotalTokenAllocation - stats.TokensSold
		stats.LicensesLeft = db.TotalLicenses - stats.LicensesSold
		stats.TotalRaised = stats.TokenRaised + stats.LicenseRaised

		jsonResponse(w, http.StatusOK, stats)
	}
}
