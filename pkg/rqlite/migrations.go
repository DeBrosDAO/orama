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

// applySQL splits the script into individual statements, strips explicit
// transaction control (BEGIN/COMMIT/ROLLBACK/END), and executes statements
// sequentially to avoid nested transaction issues with rqlite.
func applySQL(ctx context.Context, db *sql.DB, script string) error {
	s := strings.TrimSpace(script)
	if s == "" {
		return nil
	}
	stmts := splitSQLStatements(s)
	stmts = filterOutTxnControls(stmts)

	for _, stmt := range stmts {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec stmt failed: %w (stmt: %s)", err, snippet(stmt))
		}
	}
	return nil
}

func containsToken(stmts []string, token string) bool {
	for _, s := range stmts {
		if strings.EqualFold(strings.TrimSpace(s), token) {
			return true
		}
	}
	return false
}

// removed duplicate helper

// removed duplicate helper

// isTxnControl returns true if the statement is a transaction control command.
func isTxnControl(s string) bool {
	t := strings.ToUpper(strings.TrimSpace(s))
	switch t {
	case "BEGIN", "BEGIN TRANSACTION", "COMMIT", "END", "ROLLBACK":
		return true
	default:
		return false
	}
}

// filterOutTxnControls removes BEGIN/COMMIT/ROLLBACK/END statements.
func filterOutTxnControls(stmts []string) []string {
	out := make([]string, 0, len(stmts))
	for _, s := range stmts {
		if isTxnControl(s) {
			continue
		}
		out = append(out, s)
	}
	return out
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

// ApplyEmbeddedMigrations applies migrations from an embedded filesystem.
// This is the preferred method as it doesn't depend on filesystem paths.
func ApplyEmbeddedMigrations(ctx context.Context, db *sql.DB, fsys fs.FS, logger *zap.Logger) error {
	if logger == nil {
		logger = zap.NewNop()
	}

	if err := ensureMigrationsTable(ctx, db); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	files, err := readMigrationFilesFromFS(fsys)
	if err != nil {
		return fmt.Errorf("read embedded migration files: %w", err)
	}
	if len(files) == 0 {
		logger.Info("No embedded migrations found")
		return nil
	}

	applied, err := loadAppliedVersions(ctx, db)
	if err != nil {
		return fmt.Errorf("load applied versions: %w", err)
	}

	for _, mf := range files {
		if applied[mf.Version] {
			logger.Debug("Migration already applied; skipping", zap.Int("version", mf.Version), zap.String("name", mf.Name))
			continue
		}

		sqlBytes, err := fs.ReadFile(fsys, mf.Path)
		if err != nil {
			return fmt.Errorf("read embedded migration %s: %w", mf.Path, err)
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

// ApplyEmbeddedMigrations is a convenience helper bound to RQLiteManager.
func (r *RQLiteManager) ApplyEmbeddedMigrations(ctx context.Context, fsys fs.FS) error {
	db, err := sql.Open("rqlite", fmt.Sprintf("http://localhost:%d", r.config.RQLitePort))
	if err != nil {
		return fmt.Errorf("open rqlite db: %w", err)
	}
	defer db.Close()

	return ApplyEmbeddedMigrations(ctx, db, fsys, r.logger)
}

// readMigrationFilesFromFS reads migration files from an embedded filesystem.
func readMigrationFilesFromFS(fsys fs.FS) ([]migrationFile, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
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
			Path:    name, // In embedded FS, path is just the filename
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}
