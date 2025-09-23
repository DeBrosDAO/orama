package rqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	_ "github.com/rqlite/gorqlite/stdlib"
	"go.uber.org/zap"
)

// ApplyMigrations scans a directory for *.sql files, orders them by numeric prefix,
// and applies any that are not yet recorded in schema_migrations(version).
func ApplyMigrations(ctx context.Context, db *sql.DB, dir string, logger *zap.Logger) error {
	if logger == nil {
		logger = zap.NewNop()
	}

	if err := ensureMigrationsTable(ctx, db); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	files, err := readMigrationFiles(dir)
	if err != nil {
		return fmt.Errorf("read migration files: %w", err)
	}
	if len(files) == 0 {
		logger.Info("No migrations found", zap.String("dir", dir))
		return nil
	}

	applied, err := loadAppliedVersions(ctx, db)
	if err != nil {
		return fmt.Errorf("load applied versions: %w", err)
	}

	for _, mf := range files {
		if applied[mf.Version] {
			logger.Info("Migration already applied; skipping", zap.Int("version", mf.Version), zap.String("name", mf.Name))
			continue
		}

		sqlBytes, err := os.ReadFile(mf.Path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", mf.Path, err)
		}

		logger.Info("Applying migration", zap.Int("version", mf.Version), zap.String("name", mf.Name))
		if err := applySQL(ctx, db, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", mf.Version, mf.Name, err)
		}

		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, mf.Version); err != nil {
			return fmt.Errorf("record migration %d: %w", mf.Version, err)
		}
		logger.Info("Migration applied", zap.Int("version", mf.Version), zap.String("name", mf.Name))
	}

	return nil
}

// ApplyMigrationsDirs applies migrations from multiple directories.
// - Gathers *.sql files from each dir
// - Parses numeric prefix as the version
// - Errors if the same version appears in more than one dir (to avoid ambiguity)
// - Sorts globally by version and applies those not yet in schema_migrations
func ApplyMigrationsDirs(ctx context.Context, db *sql.DB, dirs []string, logger *zap.Logger) error {
	if logger == nil {
		logger = zap.NewNop()
	}
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	files, err := readMigrationFilesFromDirs(dirs)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		logger.Info("No migrations found in provided directories", zap.Strings("dirs", dirs))
		return nil
	}

	applied, err := loadAppliedVersions(ctx, db)
	if err != nil {
		return fmt.Errorf("load applied versions: %w", err)
	}

	for _, mf := range files {
		if applied[mf.Version] {
			logger.Info("Migration already applied; skipping", zap.Int("version", mf.Version), zap.String("name", mf.Name), zap.String("path", mf.Path))
			continue
		}
		sqlBytes, err := os.ReadFile(mf.Path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", mf.Path, err)
		}

		logger.Info("Applying migration", zap.Int("version", mf.Version), zap.String("name", mf.Name), zap.String("path", mf.Path))
		if err := applySQL(ctx, db, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", mf.Version, mf.Name, err)
		}

		if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, mf.Version); err != nil {
			return fmt.Errorf("record migration %d: %w", mf.Version, err)
		}
		logger.Info("Migration applied", zap.Int("version", mf.Version), zap.String("name", mf.Name))
	}

	return nil
}

// ApplyMigrationsFromManager is a convenience helper bound to RQLiteManager.
func (r *RQLiteManager) ApplyMigrations(ctx context.Context, dir string) error {
	db, err := sql.Open("rqlite", fmt.Sprintf("http://localhost:%d", r.config.RQLitePort))
	if err != nil {
		return fmt.Errorf("open rqlite db: %w", err)
	}
	defer db.Close()

	return ApplyMigrations(ctx, db, dir, r.logger)
}

// ApplyMigrationsDirs is the multi-dir variant on RQLiteManager.
func (r *RQLiteManager) ApplyMigrationsDirs(ctx context.Context, dirs []string) error {
	db, err := sql.Open("rqlite", fmt.Sprintf("http://localhost:%d", r.config.RQLitePort))
	if err != nil {
		return fmt.Errorf("open rqlite db: %w", err)
	}
	defer db.Close()

	return ApplyMigrationsDirs(ctx, db, dirs, r.logger)
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version     INTEGER PRIMARY KEY,
	applied_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
)`)
	return err
}

type migrationFile struct {
	Version int
	Name    string
	Path    string
}

func readMigrationFiles(dir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []migrationFile{}, nil
		}
		return nil, err
	}

	var out []migrationFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".sql") {
			continue
		}
		ver, ok := parseVersionPrefix(name)
		if !ok {
			continue
		}
		out = append(out, migrationFile{
			Version: ver,
			Name:    name,
			Path:    filepath.Join(dir, name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func readMigrationFilesFromDirs(dirs []string) ([]migrationFile, error) {
	all := make([]migrationFile, 0, 64)
	seen := map[int]string{} // version -> path (for duplicate detection)

	for _, d := range dirs {
		files, err := readMigrationFiles(d)
		if err != nil {
			return nil, fmt.Errorf("reading dir %s: %w", d, err)
		}
		for _, f := range files {
			if prev, dup := seen[f.Version]; dup {
				return nil, fmt.Errorf("duplicate migration version %d detected in %s and %s; ensure global version uniqueness", f.Version, prev, f.Path)
			}
			seen[f.Version] = f.Path
			all = append(all, f)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Version < all[j].Version })
	return all, nil
}

func parseVersionPrefix(name string) (int, bool) {
	// Expect formats like "001_initial.sql", "2_add_table.sql", etc.
	i := 0
	for i < len(name) && unicode.IsDigit(rune(name[i])) {
		i++
	}
	if i == 0 {
		return 0, false
	}
	ver, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0, false
	}
	return ver, true
}

func loadAppliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		// If the table doesn't exist yet (very first run), ensure it and return empty set.
		if isNoSuchTable(err) {
			if err := ensureMigrationsTable(ctx, db); err != nil {
				return nil, err
			}
			return map[int]bool{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func isNoSuchTable(err error) bool {
	// rqlite/sqlite error messages vary; keep it permissive
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist")
}

// applySQL tries to run the entire script in one Exec.
// If the driver rejects multi-statement Exec, it falls back to splitting statements and executing sequentially.
func applySQL(ctx context.Context, db *sql.DB, script string) error {
	s := strings.TrimSpace(script)
	if s == "" {
		return nil
	}
	if _, err := db.ExecContext(ctx, s); err == nil {
		return nil
	} else {
		// Fall back to splitting into statements and executing sequentially (respecting BEGIN/COMMIT if present).
		stmts := splitSQLStatements(s)
		// If the script already contains explicit BEGIN/COMMIT, we just run as-is.
		// Otherwise, we attempt to wrap in a transaction; if BeginTx fails, execute one-by-one.
		hasExplicitTxn := containsToken(stmts, "BEGIN") || containsToken(stmts, "BEGIN;")
		if !hasExplicitTxn {
			if tx, txErr := db.BeginTx(ctx, nil); txErr == nil {
				for _, stmt := range stmts {
					if stmt == "" {
						continue
					}
					if _, execErr := tx.ExecContext(ctx, stmt); execErr != nil {
						_ = tx.Rollback()
						return fmt.Errorf("exec stmt failed: %w (stmt: %s)", execErr, snippet(stmt))
					}
				}
				return tx.Commit()
			}
			// Fall through to plain sequential exec if BeginTx not supported.
		}

		for _, stmt := range stmts {
			if stmt == "" {
				continue
			}
			if _, execErr := db.ExecContext(ctx, stmt); execErr != nil {
				return fmt.Errorf("exec stmt failed: %w (stmt: %s)", execErr, snippet(stmt))
			}
		}
		return nil
	}
}

func containsToken(stmts []string, token string) bool {
	for _, s := range stmts {
		if strings.EqualFold(strings.TrimSpace(s), token) {
			return true
		}
	}
	return false
}

func snippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

// splitSQLStatements splits a SQL script into statements by semicolon, ignoring semicolons
// inside single/double-quoted strings and skipping comments (-- and /* */).
func splitSQLStatements(in string) []string {
	var out []string
	var b strings.Builder

	inLineComment := false
	inBlockComment := false
	inSingle := false
	inDouble := false

	runes := []rune(in)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}

		// Handle end of line comment
		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				// keep newline normalization but don't include comment
			}
			continue
		}
		// Handle end of block comment
		if inBlockComment {
			if ch == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		// Start of comments?
		if !inSingle && !inDouble {
			if ch == '-' && next == '-' {
				inLineComment = true
				i++
				continue
			}
			if ch == '/' && next == '*' {
				inBlockComment = true
				i++
				continue
			}
		}

		// Quotes
		if !inDouble && ch == '\'' {
			// Toggle single quotes, respecting escaped '' inside.
			if inSingle {
				// Check for escaped '' (two single quotes)
				if next == '\'' {
					b.WriteRune(ch) // write one '
					i++             // skip the next '
					continue
				}
				inSingle = false
			} else {
				inSingle = true
			}
			b.WriteRune(ch)
			continue
		}
		if !inSingle && ch == '"' {
			if inDouble {
				if next == '"' {
					b.WriteRune(ch)
					i++
					continue
				}
				inDouble = false
			} else {
				inDouble = true
			}
			b.WriteRune(ch)
			continue
		}

		// Statement boundary
		if ch == ';' && !inSingle && !inDouble {
			stmt := strings.TrimSpace(b.String())
			if stmt != "" {
				out = append(out, stmt)
			}
			b.Reset()
			continue
		}

		b.WriteRune(ch)
	}

	// Final fragment
	if s := strings.TrimSpace(b.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// Optional helper to load embedded migrations if you later decide to embed.
// Keep for future use; currently unused.
func readDirFS(fsys fs.FS, root string) ([]string, error) {
	var files []string
	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".sql") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
