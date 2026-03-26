package db

import (
	"database/sql"
	"fmt"
	"log"
)

func Migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS wallets (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		wallet     TEXT NOT NULL UNIQUE,
		chain      TEXT NOT NULL CHECK(chain IN ('sol', 'evm')),
		first_seen DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_seen  DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS token_purchases (
		id               INTEGER PRIMARY KEY AUTOINCREMENT,
		wallet           TEXT NOT NULL,
		chain            TEXT NOT NULL CHECK(chain IN ('sol', 'evm')),
		amount_paid      REAL NOT NULL,
		pay_currency     TEXT NOT NULL,
		tokens_allocated REAL NOT NULL,
		tx_hash          TEXT NOT NULL,
		created_at       DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS license_purchases (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		wallet         TEXT NOT NULL,
		chain          TEXT NOT NULL CHECK(chain IN ('sol', 'evm')),
		amount_paid    REAL NOT NULL,
		pay_currency   TEXT NOT NULL,
		tx_hash        TEXT NOT NULL,
		license_number INTEGER NOT NULL,
		claimed_via_nft INTEGER DEFAULT 0,
		created_at     DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS whitelist (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		wallet     TEXT NOT NULL UNIQUE,
		chain      TEXT NOT NULL CHECK(chain IN ('sol', 'evm')),
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS activity_log (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		event_type TEXT NOT NULL,
		wallet     TEXT NOT NULL,
		detail     TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS anchat_claims (
		id                      INTEGER PRIMARY KEY AUTOINCREMENT,
		wallet                  TEXT NOT NULL UNIQUE,
		anchat_balance_at_claim REAL NOT NULL,
		orama_amount            REAL NOT NULL,
		status                  TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'flagged', 'warned', 'revoked')),
		flagged_at              DATETIME,
		warned_at               DATETIME,
		revoked_at              DATETIME,
		claimed_at              DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS nft_license_verification (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		wallet         TEXT NOT NULL UNIQUE,
		license_id     INTEGER NOT NULL,
		status         TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'flagged', 'warned', 'revoked')),
		flagged_at     DATETIME,
		warned_at      DATETIME,
		revoked_at     DATETIME,
		created_at     DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_wallets_wallet ON wallets(wallet);
	CREATE INDEX IF NOT EXISTS idx_token_purchases_wallet ON token_purchases(wallet);
	CREATE INDEX IF NOT EXISTS idx_license_purchases_wallet ON license_purchases(wallet);
	CREATE INDEX IF NOT EXISTS idx_whitelist_wallet ON whitelist(wallet);
	CREATE INDEX IF NOT EXISTS idx_activity_log_created ON activity_log(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_anchat_claims_wallet ON anchat_claims(wallet);
	CREATE INDEX IF NOT EXISTS idx_anchat_claims_status ON anchat_claims(status);
	CREATE INDEX IF NOT EXISTS idx_nft_license_verification_wallet ON nft_license_verification(wallet);
	CREATE INDEX IF NOT EXISTS idx_nft_license_verification_status ON nft_license_verification(status);
	`
	_, err := db.Exec(schema)
	if err != nil {
		return err
	}

	// Add columns if migrating from old schema (SQLite doesn't support IF NOT EXISTS for ALTER)
	for _, col := range []struct{ table, column, def string }{
		{"anchat_claims", "status", "TEXT NOT NULL DEFAULT 'active'"},
		{"anchat_claims", "flagged_at", "DATETIME"},
		{"anchat_claims", "warned_at", "DATETIME"},
		{"anchat_claims", "revoked_at", "DATETIME"},
	} {
		db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", col.table, col.column, col.def))
	}

	return nil
}

// Seed populates initial data if tables are empty (idempotent).
func Seed(database *sql.DB) error {
	var count int
	database.QueryRow("SELECT COUNT(*) FROM token_purchases").Scan(&count)
	if count > 0 {
		return nil // Already seeded
	}

	log.Println("Seeding database with initial investor data...")

	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin seed transaction: %w", err)
	}
	defer tx.Rollback()

	// DeBros — 9JEsuEeRpxrJyFVkx1RRXBTG7V6zTeApr33nGrBpcYt3
	debrosWallet := "9JEsuEeRpxrJyFVkx1RRXBTG7V6zTeApr33nGrBpcYt3"
	tx.Exec("INSERT OR IGNORE INTO wallets (wallet, chain) VALUES (?, 'sol')", debrosWallet)
	tx.Exec(
		"INSERT INTO token_purchases (wallet, chain, amount_paid, pay_currency, tokens_allocated, tx_hash) VALUES (?, 'sol', 25000, 'SOL', 500000, 'seed-debros-token')",
		debrosWallet,
	)
	tx.Exec(
		"INSERT INTO activity_log (event_type, wallet, detail) VALUES ('token_purchase', '9JEs...pcYt3', '$25,000 — 500,000 $ORAMA')",
	)

	// ICXCNIKA — AXXGYMTVS7UGens718mKPvuWpRfX9zf4tGzsAqY5TgwZ
	icxcWallet := "AXXGYMTVS7UGens718mKPvuWpRfX9zf4tGzsAqY5TgwZ"
	tx.Exec("INSERT OR IGNORE INTO wallets (wallet, chain) VALUES (?, 'sol')", icxcWallet)

	for i := 1; i <= 3; i++ {
		tx.Exec(
			"INSERT INTO license_purchases (wallet, chain, amount_paid, pay_currency, tx_hash, license_number, claimed_via_nft) VALUES (?, 'sol', 3000, 'SOL', ?, ?, 0)",
			icxcWallet, fmt.Sprintf("seed-icxcnika-license-%d", i), i,
		)
		tx.Exec(
			"INSERT INTO activity_log (event_type, wallet, detail) VALUES ('license_purchase', 'AXXG...TgwZ', ?)",
			fmt.Sprintf("License #%d — $3,000", i),
		)
	}

	tx.Exec(
		"INSERT INTO token_purchases (wallet, chain, amount_paid, pay_currency, tokens_allocated, tx_hash) VALUES (?, 'sol', 1000, 'SOL', 20000, 'seed-icxcnika-token')",
		icxcWallet,
	)
	tx.Exec(
		"INSERT INTO activity_log (event_type, wallet, detail) VALUES ('token_purchase', 'AXXG...TgwZ', '$1,000 — 20,000 $ORAMA')",
	)

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit seed data: %w", err)
	}

	log.Println("Seed data inserted: DeBros ($25K tokens) + ICXCNIKA (3 licenses + $1K tokens)")
	return nil
}
