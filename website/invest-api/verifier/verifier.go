package verifier

import (
	"context"
	"database/sql"
	"log"
	"time"

	dbpkg "github.com/debros/orama-website/invest-api/db"
	"github.com/debros/orama-website/invest-api/helius"
)

const (
	SweepInterval = 30 * time.Minute
	GracePeriod   = 5 * time.Minute
)

// Verifier periodically checks that claim holders still hold their assets.
// State machine: active → flagged → warned → revoked (each transition requires
// the asset to still be missing after GracePeriod).
type Verifier struct {
	db     *sql.DB
	helius *helius.Client
}

func New(db *sql.DB, heliusClient *helius.Client) *Verifier {
	return &Verifier{db: db, helius: heliusClient}
}

// Run starts the background sweep loop. Blocks until ctx is cancelled.
func (v *Verifier) Run(ctx context.Context) {
	log.Println("[verifier] starting background verification (interval:", SweepInterval, ")")

	// Run once on startup to catch anything missed during downtime
	v.sweep()

	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[verifier] shutting down")
			return
		case <-ticker.C:
			v.sweep()
		}
	}
}

func (v *Verifier) sweep() {
	log.Println("[verifier] starting sweep...")
	v.verifyAnchatClaims()
	v.verifyNftLicenses()
	log.Println("[verifier] sweep complete")
}

// ── $ANCHAT claim verification ──

func (v *Verifier) verifyAnchatClaims() {
	rows, err := v.db.Query(
		"SELECT id, wallet, status, flagged_at, warned_at FROM anchat_claims WHERE status != 'revoked'",
	)
	if err != nil {
		log.Printf("[verifier] failed to query anchat_claims: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var wallet, status string
		var flaggedAt, warnedAt sql.NullString

		if err := rows.Scan(&id, &wallet, &status, &flaggedAt, &warnedAt); err != nil {
			log.Printf("[verifier] scan error: %v", err)
			continue
		}

		balance, err := v.helius.GetTokenBalanceFresh(wallet, dbpkg.AnchatMint)
		if err != nil {
			log.Printf("[verifier] helius error for %s: %v", truncate(wallet), err)
			continue // Don't change state on RPC errors
		}

		holdsAsset := balance >= dbpkg.AnchatMinBalance
		v.updateState(id, "anchat_claims", wallet, status, holdsAsset, flaggedAt, warnedAt, "anchat_claim")
	}
}

// ── NFT license verification ──

func (v *Verifier) verifyNftLicenses() {
	rows, err := v.db.Query(
		"SELECT id, wallet, status, flagged_at, warned_at FROM nft_license_verification WHERE status != 'revoked'",
	)
	if err != nil {
		log.Printf("[verifier] failed to query nft_license_verification: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var wallet, status string
		var flaggedAt, warnedAt sql.NullString

		if err := rows.Scan(&id, &wallet, &status, &flaggedAt, &warnedAt); err != nil {
			log.Printf("[verifier] scan error: %v", err)
			continue
		}

		nftResult, err := v.helius.CheckNFTs(wallet, dbpkg.TeamNFTCollection, dbpkg.CommunityNFTCollection)
		if err != nil {
			log.Printf("[verifier] helius NFT error for %s: %v", truncate(wallet), err)
			continue
		}

		holdsAsset := nftResult.HasTeamNFT
		v.updateState(id, "nft_license_verification", wallet, status, holdsAsset, flaggedAt, warnedAt, "nft_license")
	}
}

// ── State machine logic ──

func (v *Verifier) updateState(
	id int,
	table, wallet, currentStatus string,
	holdsAsset bool,
	flaggedAt, warnedAt sql.NullString,
	claimType string,
) {
	now := time.Now()

	if holdsAsset {
		// Asset is back — reset to active
		if currentStatus != "active" {
			v.db.Exec(
				"UPDATE "+table+" SET status = 'active', flagged_at = NULL, warned_at = NULL, revoked_at = NULL WHERE id = ?",
				id,
			)
			log.Printf("[verifier] %s %s: RESTORED to active (asset found again)", table, truncate(wallet))
			v.logActivity(claimType+"_restored", wallet, "Asset detected, claim restored")
		}
		return
	}

	// Asset NOT found — advance state machine
	switch currentStatus {
	case "active":
		// First miss → flag
		v.db.Exec("UPDATE "+table+" SET status = 'flagged', flagged_at = ? WHERE id = ?", now.Format(time.RFC3339), id)
		log.Printf("[verifier] %s %s: FLAGGED (asset not found, check 1/3)", table, truncate(wallet))

	case "flagged":
		// Second miss — check grace period
		if !flaggedAt.Valid {
			return
		}
		flagTime, err := time.Parse(time.RFC3339, flaggedAt.String)
		if err != nil {
			return
		}
		if now.Sub(flagTime) < GracePeriod {
			return // Too soon, wait
		}
		v.db.Exec("UPDATE "+table+" SET status = 'warned', warned_at = ? WHERE id = ?", now.Format(time.RFC3339), id)
		log.Printf("[verifier] %s %s: WARNED (asset not found, check 2/3)", table, truncate(wallet))

	case "warned":
		// Third miss — check grace period from warned_at
		if !warnedAt.Valid {
			return
		}
		warnTime, err := time.Parse(time.RFC3339, warnedAt.String)
		if err != nil {
			return
		}
		if now.Sub(warnTime) < GracePeriod {
			return
		}
		// REVOKE
		v.db.Exec("UPDATE "+table+" SET status = 'revoked', revoked_at = ? WHERE id = ?", now.Format(time.RFC3339), id)
		log.Printf("[verifier] %s %s: REVOKED (asset not found, check 3/3)", table, truncate(wallet))
		v.logActivity(claimType+"_revoked", wallet, "Claim revoked — asset no longer held")

		// For NFT licenses: also mark the license_purchase as revoked
		if table == "nft_license_verification" {
			v.db.Exec(
				"UPDATE license_purchases SET claimed_via_nft = -1 WHERE wallet = ? AND claimed_via_nft = 1",
				wallet,
			)
		}
	}
}

func (v *Verifier) logActivity(eventType, wallet, detail string) {
	truncated := truncate(wallet)
	v.db.Exec(
		"INSERT INTO activity_log (event_type, wallet, detail) VALUES (?, ?, ?)",
		eventType, truncated, detail,
	)
}

func truncate(wallet string) string {
	if len(wallet) > 10 {
		return wallet[:6] + "..." + wallet[len(wallet)-4:]
	}
	return wallet
}
