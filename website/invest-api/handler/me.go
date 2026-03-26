package handler

import (
	"database/sql"
	"net/http"

	"github.com/debros/orama-website/invest-api/auth"
	"github.com/debros/orama-website/invest-api/db"
)

func MeHandler(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wallet, chain, ok := auth.WalletFromContext(r.Context())
		if !ok {
			jsonError(w, http.StatusUnauthorized, "wallet not found in context")
			return
		}

		resp := db.MeResponse{
			Wallet:          wallet,
			Chain:            chain,
			Licenses:        []db.LicenseInfo{},
			PurchaseHistory: []db.PurchaseRecord{},
		}

		// Token totals
		database.QueryRow(
			"SELECT COALESCE(SUM(tokens_allocated), 0), COALESCE(SUM(amount_paid), 0) FROM token_purchases WHERE wallet = ?",
			wallet,
		).Scan(&resp.TokensPurchased, &resp.TokenSpent)

		// Licenses
		rows, _ := database.Query(
			"SELECT license_number, claimed_via_nft, created_at FROM license_purchases WHERE wallet = ? ORDER BY created_at",
			wallet,
		)
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var l db.LicenseInfo
				var claimed int
				rows.Scan(&l.LicenseNumber, &claimed, &l.PurchasedAt)
				l.ClaimedViaNFT = claimed == 1
				resp.Licenses = append(resp.Licenses, l)
			}
		}

		// Whitelist
		var count int
		database.QueryRow("SELECT COUNT(*) FROM whitelist WHERE wallet = ?", wallet).Scan(&count)
		resp.OnWhitelist = count > 0

		// ANCHAT claim status
		var claimCount int
		database.QueryRow("SELECT COUNT(*) FROM anchat_claims WHERE wallet = ?", wallet).Scan(&claimCount)
		resp.AnchatClaimed = claimCount > 0
		if resp.AnchatClaimed {
			database.QueryRow("SELECT orama_amount FROM anchat_claims WHERE wallet = ?", wallet).Scan(&resp.AnchatOrama)
		}

		// Purchase history (combined)
		histRows, _ := database.Query(`
			SELECT 'token' as type, amount_paid, pay_currency, tx_hash, created_at FROM token_purchases WHERE wallet = ?
			UNION ALL
			SELECT 'license' as type, amount_paid, pay_currency, tx_hash, created_at FROM license_purchases WHERE wallet = ?
			ORDER BY created_at DESC
		`, wallet, wallet)
		if histRows != nil {
			defer histRows.Close()
			for histRows.Next() {
				var p db.PurchaseRecord
				histRows.Scan(&p.Type, &p.Amount, &p.Currency, &p.TxHash, &p.CreatedAt)
				resp.PurchaseHistory = append(resp.PurchaseHistory, p)
			}
		}

		jsonResponse(w, http.StatusOK, resp)
	}
}
