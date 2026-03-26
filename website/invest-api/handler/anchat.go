package handler

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"

	"github.com/debros/orama-website/invest-api/auth"
	dbpkg "github.com/debros/orama-website/invest-api/db"
	"github.com/debros/orama-website/invest-api/helius"
)

func AnchatBalanceHandler(database *sql.DB, heliusClient *helius.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wallet, chain, ok := auth.WalletFromContext(r.Context())
		if !ok {
			jsonError(w, http.StatusUnauthorized, "wallet required")
			return
		}

		if chain != "sol" {
			jsonError(w, http.StatusBadRequest, "$ANCHAT is a Solana token — connect with a Solana wallet")
			return
		}

		// Check current claim status
		var status string
		err := database.QueryRow("SELECT status FROM anchat_claims WHERE wallet = ?", wallet).Scan(&status)
		hasActiveClaim := err == nil && status == "active"
		hasRevokedClaim := err == nil && status == "revoked"

		balance, err := heliusClient.GetTokenBalance(wallet, dbpkg.AnchatMint)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, fmt.Sprintf("failed to check $ANCHAT balance: %v", err))
			return
		}

		claimable := 0.0
		canClaim := false
		if balance >= dbpkg.AnchatMinBalance && !hasActiveClaim {
			claimable = math.Floor(balance * dbpkg.AnchatClaimRate)
			canClaim = true
		}

		jsonResponse(w, http.StatusOK, map[string]any{
			"balance":         balance,
			"claimable_orama": claimable,
			"already_claimed": hasActiveClaim,
			"was_revoked":     hasRevokedClaim,
			"can_claim":       canClaim,
			"min_balance":     dbpkg.AnchatMinBalance,
		})
	}
}

func AnchatClaimHandler(database *sql.DB, heliusClient *helius.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wallet, chain, ok := auth.WalletFromContext(r.Context())
		if !ok {
			jsonError(w, http.StatusUnauthorized, "wallet required")
			return
		}

		if chain != "sol" {
			jsonError(w, http.StatusBadRequest, "$ANCHAT is a Solana token — connect with a Solana wallet")
			return
		}

		// Check if there's an active claim already
		var status string
		err := database.QueryRow("SELECT status FROM anchat_claims WHERE wallet = ?", wallet).Scan(&status)
		if err == nil && status == "active" {
			jsonError(w, http.StatusConflict, "you already have an active $ORAMA claim")
			return
		}

		// Fresh balance (bypass cache)
		balance, err := heliusClient.GetTokenBalanceFresh(wallet, dbpkg.AnchatMint)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, fmt.Sprintf("failed to verify $ANCHAT balance: %v", err))
			return
		}

		if balance < dbpkg.AnchatMinBalance {
			jsonError(w, http.StatusBadRequest,
				fmt.Sprintf("minimum %s $ANCHAT required (you have %.0f)",
					formatInt(dbpkg.AnchatMinBalance), balance))
			return
		}

		oramaAmount := math.Floor(balance * dbpkg.AnchatClaimRate)
		if oramaAmount <= 0 {
			jsonError(w, http.StatusBadRequest, "$ANCHAT balance too low to claim $ORAMA")
			return
		}

		// Upsert: if previously revoked, update to active; otherwise insert
		_, err = database.Exec(`
			INSERT INTO anchat_claims (wallet, anchat_balance_at_claim, orama_amount, status, flagged_at, warned_at, revoked_at)
			VALUES (?, ?, ?, 'active', NULL, NULL, NULL)
			ON CONFLICT(wallet) DO UPDATE SET
				anchat_balance_at_claim = ?,
				orama_amount = ?,
				status = 'active',
				flagged_at = NULL,
				warned_at = NULL,
				revoked_at = NULL,
				claimed_at = CURRENT_TIMESTAMP
		`, wallet, balance, oramaAmount, balance, oramaAmount)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to record claim")
			return
		}

		logActivity(database, "anchat_claim", wallet, fmt.Sprintf("%.0f $ANCHAT → %.0f $ORAMA", balance, oramaAmount))

		jsonResponse(w, http.StatusCreated, map[string]any{
			"orama_amount": oramaAmount,
		})
	}
}

func formatInt(n float64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.0fK", n/1000)
	}
	return fmt.Sprintf("%.0f", n)
}
