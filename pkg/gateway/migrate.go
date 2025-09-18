package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

var errNoMigrationsFound = errors.New("no migrations found")

func (g *Gateway) applyAutoMigrations(ctx context.Context) error {
	if g.client == nil {
		return nil
	}
	db := g.client.Database()

	// Use internal context to bypass authentication for system migrations
	internalCtx := client.WithInternalAuth(ctx)

	stmts := []string{
		// namespaces
		"CREATE TABLE IF NOT EXISTS namespaces (\n\t id INTEGER PRIMARY KEY AUTOINCREMENT,\n\t name TEXT NOT NULL UNIQUE,\n\t created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP\n)",
		// api_keys
		"CREATE TABLE IF NOT EXISTS api_keys (\n\t id INTEGER PRIMARY KEY AUTOINCREMENT,\n\t key TEXT NOT NULL UNIQUE,\n\t name TEXT,\n\t namespace_id INTEGER NOT NULL,\n\t scopes TEXT,\n\t created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,\n\t last_used_at TIMESTAMP,\n\t FOREIGN KEY(namespace_id) REFERENCES namespaces(id) ON DELETE CASCADE\n)",
		"CREATE INDEX IF NOT EXISTS idx_api_keys_namespace ON api_keys(namespace_id)",
		// request_logs
		"CREATE TABLE IF NOT EXISTS request_logs (\n\t id INTEGER PRIMARY KEY AUTOINCREMENT,\n\t method TEXT NOT NULL,\n\t path TEXT NOT NULL,\n\t status_code INTEGER NOT NULL,\n\t bytes_out INTEGER NOT NULL DEFAULT 0,\n\t duration_ms INTEGER NOT NULL DEFAULT 0,\n\t ip TEXT,\n\t api_key_id INTEGER,\n\t created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,\n\t FOREIGN KEY(api_key_id) REFERENCES api_keys(id) ON DELETE SET NULL\n)",
		"CREATE INDEX IF NOT EXISTS idx_request_logs_api_key ON request_logs(api_key_id)",
		"CREATE INDEX IF NOT EXISTS idx_request_logs_created_at ON request_logs(created_at)",
		// seed default namespace
		"INSERT OR IGNORE INTO namespaces(name) VALUES ('default')",
	}

	for _, stmt := range stmts {
		if _, err := db.Query(internalCtx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (g *Gateway) applyMigrations(ctx context.Context) error {
	if g.client == nil {
		return nil
	}
	db := g.client.Database()

	// Use internal context to bypass authentication for system migrations
	internalCtx := client.WithInternalAuth(ctx)

	// Ensure schema_migrations exists first
	if _, err := db.Query(internalCtx, "CREATE TABLE IF NOT EXISTS schema_migrations (\n\tversion INTEGER PRIMARY KEY,\n\tapplied_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))\n)"); err != nil {
		return err
	}

	// Locate migrations directory relative to CWD
	migDir := "migrations"
	if fi, err := os.Stat(migDir); err != nil || !fi.IsDir() {
		return errNoMigrationsFound
	}

	entries, err := os.ReadDir(migDir)
	if err != nil {
		return err
	}
	type mig struct {
		ver  int
		path string
	}
	migrations := make([]mig, 0)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".sql") {
			continue
		}
		if ver, ok := parseMigrationVersion(name); ok {
			migrations = append(migrations, mig{ver: ver, path: filepath.Join(migDir, name)})
		}
	}
	if len(migrations) == 0 {
		return errNoMigrationsFound
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].ver < migrations[j].ver })

	// Helper to check if version applied
	isApplied := func(ctx context.Context, v int) (bool, error) {
		res, err := db.Query(ctx, "SELECT 1 FROM schema_migrations WHERE version = ? LIMIT 1", v)
		if err != nil {
			return false, err
		}
		return res != nil && res.Count > 0, nil
	}

	for _, m := range migrations {
		applied, err := isApplied(internalCtx, m.ver)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		// Read and split SQL file into statements
		content, err := os.ReadFile(m.path)
		if err != nil {
			return err
		}
		stmts := splitSQLStatements(string(content))
		for _, s := range stmts {
			if s == "" {
				continue
			}
			if _, err := db.Query(internalCtx, s); err != nil {
				return err
			}
		}
		// Mark as applied
		if _, err := db.Query(internalCtx, "INSERT INTO schema_migrations (version) VALUES (?)", m.ver); err != nil {
			return err
		}
		g.logger.ComponentInfo(logging.ComponentDatabase, "applied migration", zap.Int("version", m.ver), zap.String("file", m.path))
	}
	return nil
}

func parseMigrationVersion(name string) (int, bool) {
	i := 0
	for i < len(name) && name[i] >= '0' && name[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, false
	}
	v, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0, false
	}
	return v, true
}

func splitSQLStatements(sqlText string) []string {
	lines := strings.Split(sqlText, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, ln := range lines {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		// Handle inline comments by removing everything after --
		if commentIdx := strings.Index(s, "--"); commentIdx >= 0 {
			s = strings.TrimSpace(s[:commentIdx])
			if s == "" {
				continue // line was only a comment
			}
		}
		upper := strings.ToUpper(s)
		if upper == "BEGIN;" || upper == "COMMIT;" || upper == "BEGIN" || upper == "COMMIT" {
			continue
		}
		if strings.HasPrefix(upper, "INSERT") && strings.Contains(upper, "SCHEMA_MIGRATIONS") {
			// ignore in-file migration markers
			continue
		}
		cleaned = append(cleaned, s)
	}
	// Join and split by ';'
	joined := strings.Join(cleaned, "\n")
	parts := strings.Split(joined, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		sp := strings.TrimSpace(p)
		if sp == "" {
			continue
		}
		out = append(out, sp+";")
	}
	return out
}
