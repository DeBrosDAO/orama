// Package migrations holds the embedded SQL migrations for the gateway's
// RQLite registry. This file defines the schema-version contract every
// gateway binary must enforce at startup.
//
// The contract:
//
//  1. The binary embeds every migration file in this directory.
//  2. RequiredVersion() returns the highest numbered migration in the embed.
//     This is the schema version the binary REQUIRES to function correctly.
//  3. AssertSchema(ctx, db) queries the schema_migrations table and returns
//     a typed *SchemaMismatchError if the applied version is below
//     RequiredVersion. Gateway startup MUST treat this as fatal.
//
// Why: a rolling upgrade can swap the gateway binary without restarting the
// underlying RQLite process. If a new binary expects columns added by a
// migration the RQLite-process startup never re-ran, INSERTs fail with
// cryptic errors at runtime. Asserting the contract at startup catches the
// mismatch immediately with an actionable error message.
//
// See plan: this file is the long-term fix for the AnChat-test "missing
// ws_max_frame_bytes column" incident (2026-05-06).
package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// MigrationInfo describes one embedded migration.
type MigrationInfo struct {
	Version int
	Name    string
	Path    string
}

// allMigrations returns every embedded migration sorted by version ascending.
// Computed once at startup; cheap to call repeatedly.
var allMigrations = mustListMigrations()

func mustListMigrations() []MigrationInfo {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		// In practice this can't happen — the embed.FS is built from a
		// known directory. If it does, we can't safely run anything.
		panic(fmt.Sprintf("migrations: failed to list embedded files: %v", err))
	}

	var out []MigrationInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		v, ok := parseVersion(name)
		if !ok {
			continue
		}
		out = append(out, MigrationInfo{
			Version: v,
			Name:    strings.TrimSuffix(name, ".sql"),
			Path:    name,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}

// parseVersion extracts the integer prefix from "001_initial.sql" → 1.
// Returns ok=false for files without a leading numeric prefix.
func parseVersion(filename string) (int, bool) {
	idx := strings.IndexByte(filename, '_')
	if idx <= 0 {
		return 0, false
	}
	v, err := strconv.Atoi(filename[:idx])
	if err != nil {
		return 0, false
	}
	return v, true
}

// All returns a snapshot of every embedded migration, sorted by version.
// The returned slice is a copy; safe to mutate.
func All() []MigrationInfo {
	out := make([]MigrationInfo, len(allMigrations))
	copy(out, allMigrations)
	return out
}

// RequiredVersion returns the highest migration version embedded in this
// binary. Panics if no migrations are embedded (impossible in practice).
//
// This is the schema version the binary requires. The gateway asserts at
// startup that the database's applied schema is >= this value.
func RequiredVersion() int {
	if len(allMigrations) == 0 {
		panic("migrations: no embedded migrations found")
	}
	return allMigrations[len(allMigrations)-1].Version
}

// SchemaMismatchError is returned when the database's applied schema is
// behind what the binary requires. Gateway startup MUST treat this as fatal
// and log the actionable hint.
type SchemaMismatchError struct {
	RequiredVersion int
	AppliedVersion  int
	Pending         []MigrationInfo // migrations the binary has but the DB lacks
}

func (e *SchemaMismatchError) Error() string {
	pending := make([]string, 0, len(e.Pending))
	for _, m := range e.Pending {
		pending = append(pending, fmt.Sprintf("%03d (%s)", m.Version, m.Name))
	}
	return fmt.Sprintf(
		"schema mismatch: binary requires version %d, database has %d. "+
			"Pending migrations: [%s]. "+
			"Run `orama node migrate-apply` on the namespace's RQLite to fix.",
		e.RequiredVersion, e.AppliedVersion, strings.Join(pending, ", "),
	)
}

// AppliedVersion queries the schema_migrations table and returns the highest
// version recorded as applied. Returns 0 (with nil error) if the table is
// empty — that's a fresh database, valid state.
//
// Returns an error if the schema_migrations table itself doesn't exist or
// can't be read; callers must distinguish that from "applied=0".
func AppliedVersion(ctx context.Context, db *sql.DB) (int, error) {
	row := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`)
	var v int
	if err := row.Scan(&v); err != nil {
		return 0, fmt.Errorf("migrations: query schema_migrations: %w", err)
	}
	return v, nil
}

// AssertSchema verifies the database's applied schema is at least
// RequiredVersion(). Returns nil on match-or-newer, *SchemaMismatchError
// on lag.
//
// Newer-than-required is OK — that means an older binary is talking to a
// database that's been advanced by a newer binary in the cluster. The
// binary just won't use whatever the newer columns enable. (Gateway
// startup should still allow this; it's a normal rolling-upgrade window.)
func AssertSchema(ctx context.Context, db *sql.DB) error {
	required := RequiredVersion()
	applied, err := AppliedVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("migrations.AssertSchema: %w", err)
	}
	if applied >= required {
		return nil
	}

	// Compute pending migrations for the error message.
	pending := make([]MigrationInfo, 0)
	for _, m := range allMigrations {
		if m.Version > applied {
			pending = append(pending, m)
		}
	}
	return &SchemaMismatchError{
		RequiredVersion: required,
		AppliedVersion:  applied,
		Pending:         pending,
	}
}

// PendingMigrations returns migrations the binary has but the database
// hasn't applied. Used by the `orama node migrate-status` CLI to show
// the operator what would be applied by a `migrate-apply`.
func PendingMigrations(ctx context.Context, db *sql.DB) ([]MigrationInfo, error) {
	applied, err := AppliedVersion(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]MigrationInfo, 0)
	for _, m := range allMigrations {
		if m.Version > applied {
			out = append(out, m)
		}
	}
	return out, nil
}
