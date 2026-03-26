package handler

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/debros/orama-website/invest-api/auth"
	"github.com/debros/orama-website/invest-api/db"
	"github.com/debros/orama-website/invest-api/helius"
)

type TokenPurchaseRequest struct {
	AmountPaid  float64 `json:"amount_paid"`
	PayCurrency string  `json:"pay_currency"`
	TxHash      string  `json:"tx_hash"`
}

type LicensePurchaseRequest struct {
	AmountPaid    float64 `json:"amount_paid"`
	PayCurrency   string  `json:"pay_currency"`
	TxHash        string  `json:"tx_hash"`
	ClaimedViaNFT bool    `json:"claimed_via_nft"`
}

func TokenPurchaseHandler(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wallet, chain, ok := auth.WalletFromContext(r.Context())
		if !ok {
			jsonError(w, http.StatusUnauthorized, "wallet required")
			return
		}

		var req TokenPurchaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if req.TxHash == "" {
			jsonError(w, http.StatusBadRequest, "tx_hash is required")
			return
		}
		if req.AmountPaid < db.MinTokenPurchaseUSD {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("minimum purchase is $%.0f", db.MinTokenPurchaseUSD))
			return
		}

		tokensAllocated := req.AmountPaid / db.TokenPrice

		var sold float64
		database.QueryRow("SELECT COALESCE(SUM(tokens_allocated), 0) FROM token_purchases").Scan(&sold)
		if sold+tokensAllocated > db.TotalTokenAllocation {
			jsonError(w, http.StatusConflict, "not enough tokens remaining in pre-sale allocation")
			return
		}

		var existing int
		database.QueryRow("SELECT COUNT(*) FROM token_purchases WHERE tx_hash = ?", req.TxHash).Scan(&existing)
		if existing > 0 {
			jsonError(w, http.StatusConflict, "transaction already recorded")
			return
		}

		_, err := database.Exec(
			"INSERT INTO token_purchases (wallet, chain, amount_paid, pay_currency, tokens_allocated, tx_hash) VALUES (?, ?, ?, ?, ?, ?)",
			wallet, chain, req.AmountPaid, req.PayCurrency, tokensAllocated, req.TxHash,
		)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to record purchase")
			return
		}

		logActivity(database, "token_purchase", wallet, fmt.Sprintf("%.0f $ORAMA for $%.2f %s", tokensAllocated, req.AmountPaid, req.PayCurrency))

		jsonResponse(w, http.StatusCreated, map[string]any{
			"tokens_allocated": tokensAllocated,
			"tx_hash":          req.TxHash,
		})
	}
}

func LicensePurchaseHandler(database *sql.DB, heliusClient *helius.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wallet, chain, ok := auth.WalletFromContext(r.Context())
		if !ok {
			jsonError(w, http.StatusUnauthorized, "wallet required")
			return
		}

		var req LicensePurchaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		// NFT claim: verify on-chain ownership
		if req.ClaimedViaNFT {
			if chain != "sol" {
				jsonError(w, http.StatusBadRequest, "NFT claims require a Solana wallet")
				return
			}

			// Check for existing active NFT claim (allow re-claim if revoked)
			var nftClaimStatus string
			claimErr := database.QueryRow(
				"SELECT status FROM nft_license_verification WHERE wallet = ?", wallet,
			).Scan(&nftClaimStatus)
			if claimErr == nil && nftClaimStatus == "active" {
				jsonError(w, http.StatusConflict, "you already have an active free license claim")
				return
			}

			// Server-side verification via Helius
			nftResult, err := heliusClient.CheckNFTs(wallet, db.TeamNFTCollection, db.CommunityNFTCollection)
			if err != nil {
				jsonError(w, http.StatusInternalServerError, fmt.Sprintf("failed to verify NFT ownership: %v", err))
				return
			}
			if !nftResult.HasTeamNFT {
				jsonError(w, http.StatusForbidden, "wallet does not hold a DeBros Team NFT")
				return
			}
		} else {
			if req.TxHash == "" {
				jsonError(w, http.StatusBadRequest, "tx_hash is required")
				return
			}
			if req.AmountPaid < db.LicensePrice {
				jsonError(w, http.StatusBadRequest, fmt.Sprintf("license costs $%.0f", db.LicensePrice))
				return
			}
		}

		var sold int
		database.QueryRow("SELECT COUNT(*) FROM license_purchases").Scan(&sold)
		if sold >= db.TotalLicenses {
			jsonError(w, http.StatusConflict, "all licenses have been sold")
			return
		}

		licenseNumber := sold + 1
		claimedInt := 0
		if req.ClaimedViaNFT {
			claimedInt = 1
			req.AmountPaid = 0
			req.PayCurrency = "nft_claim"
			req.TxHash = fmt.Sprintf("nft-claim-%s-%d", wallet[:8], time.Now().Unix())
		}

		if !req.ClaimedViaNFT {
			var existing int
			database.QueryRow("SELECT COUNT(*) FROM license_purchases WHERE tx_hash = ?", req.TxHash).Scan(&existing)
			if existing > 0 {
				jsonError(w, http.StatusConflict, "transaction already recorded")
				return
			}
		}

		_, err := database.Exec(
			"INSERT INTO license_purchases (wallet, chain, amount_paid, pay_currency, tx_hash, license_number, claimed_via_nft) VALUES (?, ?, ?, ?, ?, ?, ?)",
			wallet, chain, req.AmountPaid, req.PayCurrency, req.TxHash, licenseNumber, claimedInt,
		)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to record license purchase")
			return
		}

		detail := fmt.Sprintf("License #%d", licenseNumber)
		if req.ClaimedViaNFT {
			detail += " (DeBros NFT claim)"

			// Register in verification table for periodic checks
			database.Exec(`
				INSERT INTO nft_license_verification (wallet, license_id, status)
				VALUES (?, ?, 'active')
				ON CONFLICT(wallet) DO UPDATE SET
					license_id = ?,
					status = 'active',
					flagged_at = NULL,
					warned_at = NULL,
					revoked_at = NULL
			`, wallet, licenseNumber, licenseNumber)
		}
		logActivity(database, "license_purchase", wallet, detail)

		jsonResponse(w, http.StatusCreated, map[string]any{
			"license_number":  licenseNumber,
			"claimed_via_nft": req.ClaimedViaNFT,
		})
	}
}
