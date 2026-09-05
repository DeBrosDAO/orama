// Package node — schema subcommand. Operator-facing commands for
// inspecting and applying the embedded gateway schema migrations against
// the local RQLite instance.
//
// `orama node schema status` — non-destructive: shows binary's required
//
//	schema version, applied version, and pending
//	migrations. Useful in rolling-upgrade
//	monitoring.
//
// `orama node schema apply`  — applies any pending migrations. Idempotent
//
//	and safe to re-run; ALTER TABLE failures for
//	existing columns are tolerated. Confirms
//	before running unless --yes is passed.
//
// These are the long-term fix for the "schema lag after gateway-only
// upgrade" class of incident. See migrations/contract.go for the contract.
package node

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/constants"
	_ "github.com/rqlite/gorqlite/stdlib"
)

var (
	schemaDSN string
	schemaYes bool
)

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Inspect and apply gateway schema migrations against the local RQLite",
	Long: `Schema lifecycle commands.

The gateway binary embeds a set of SQL migrations. Each migration is numbered;
the highest number is the schema version the binary requires. After deploying
a new gateway binary, run 'orama node schema apply' on every namespace's RQLite
to bring the schema up to date — otherwise function deploys fail at runtime
with cryptic missing-column errors.`,
}

var schemaStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show required vs applied schema version + pending migrations",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, dsn, err := openSchemaDB()
		if err != nil {
			return err
		}
		defer db.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		applied, err := migrations.AppliedVersion(ctx, db)
		if err != nil {
			return fmt.Errorf("query applied version: %w", err)
		}
		required := migrations.RequiredVersion()
		pending, err := migrations.PendingMigrations(ctx, db)
		if err != nil {
			return fmt.Errorf("compute pending: %w", err)
		}

		fmt.Printf("Connection:        %s\n", dsn)
		fmt.Printf("Required version:  %d  (highest migration in binary)\n", required)
		fmt.Printf("Applied version:   %d\n", applied)
		switch {
		case applied == required:
			fmt.Printf("Status:            ✓ up to date\n")
		case applied > required:
			fmt.Printf("Status:            ⚠ database AHEAD of binary (%d > %d) — newer binary in cluster?\n",
				applied, required)
		default:
			fmt.Printf("Status:            ✗ BEHIND — %d migration(s) pending\n", len(pending))
		}

		if len(pending) > 0 {
			fmt.Println("\nPending migrations:")
			for _, m := range pending {
				fmt.Printf("  %03d  %s\n", m.Version, m.Name)
			}
			fmt.Println("\nRun 'sudo orama node schema apply' to apply them.")
		}
		return nil
	},
}

var schemaApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply pending migrations to the local RQLite",
	Long: `Apply every embedded migration not yet recorded in schema_migrations.

ALTER TABLE statements that target an already-existing column are tolerated
(the migration is marked complete). Other errors abort the run with the
schema in a partially-applied state — re-running is safe because each
migration is independently versioned.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, dsn, err := openSchemaDB()
		if err != nil {
			return err
		}
		defer db.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		pending, err := migrations.PendingMigrations(ctx, db)
		if err != nil {
			return fmt.Errorf("compute pending: %w", err)
		}
		if len(pending) == 0 {
			fmt.Printf("No pending migrations. Schema is at version %d.\n", migrations.RequiredVersion())
			return nil
		}

		fmt.Printf("Will apply %d migration(s) to %s:\n", len(pending), dsn)
		for _, m := range pending {
			fmt.Printf("  %03d  %s\n", m.Version, m.Name)
		}

		if !schemaYes {
			fmt.Print("\nProceed? [y/N]: ")
			var ans string
			_, _ = fmt.Scanln(&ans)
			if strings.ToLower(strings.TrimSpace(ans)) != "y" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		// Use the existing migration runner — it does the same thing the
		// gateway does at startup, with idempotent-error tolerance.
		logger, _ := zap.NewProduction()
		defer func() { _ = logger.Sync() }()

		if err := rqlite.ApplyEmbeddedMigrations(ctx, db, migrations.FS, logger); err != nil {
			return fmt.Errorf("apply failed: %w", err)
		}

		// Verify post-apply.
		if err := migrations.AssertSchema(ctx, db); err != nil {
			return fmt.Errorf("apply completed but schema still lags: %w", err)
		}

		fmt.Printf("\n✓ Schema now at version %d.\n", migrations.RequiredVersion())
		return nil
	},
}

// openSchemaDB returns a *sql.DB connected to the local RQLite instance,
// using the --dsn flag if provided, else discovering from the node config
// or falling back to the index RQLite port on localhost.
func openSchemaDB() (*sql.DB, string, error) {
	dsn := schemaDSN
	if dsn == "" {
		dsn = discoverLocalRQLiteDSN()
	}
	db, err := sql.Open("rqlite", dsn)
	if err != nil {
		return nil, "", fmt.Errorf("open rqlite: %w", err)
	}
	// Quick liveness check so we fail fast with a clear error.
	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, "", fmt.Errorf("rqlite at %s unreachable: %w "+
			"(hint: is RQLite running? try 'orama node status')", dsn, err)
	}
	return db, dsn, nil
}

// discoverLocalRQLiteDSN reads the node config to find the local RQLite
// port + credentials, falling back to the index RQLite port with no auth.
func discoverLocalRQLiteDSN() string {
	fallback := constants.LocalRQLiteURL()

	cfgPath, err := config.DefaultPath("node.yaml")
	if err != nil {
		return fallback
	}
	if _, err := os.Stat(cfgPath); err != nil {
		return fallback
	}
	cfgDir := filepath.Dir(cfgPath)

	// Try to read RQLite credentials from the standard secrets path.
	user, pass := readRQLiteCreds(cfgDir)

	port := readRQLitePortFromConfig(cfgPath)
	if port == 0 {
		port = constants.RQLiteHTTPPort
	}
	if user == "" {
		return fmt.Sprintf("http://localhost:%d", port)
	}
	return fmt.Sprintf("http://%s:%s@localhost:%d", user, pass, port)
}

// readRQLiteCreds best-effort reads the user:pass from secrets files
// adjacent to the node config. Returns ("","") on any miss; the caller
// then connects without auth (which works on a local-only instance).
func readRQLiteCreds(cfgDir string) (string, string) {
	type pair struct{ userFile, passFile string }
	candidates := []pair{
		{filepath.Join(cfgDir, "secrets", "rqlite-user"), filepath.Join(cfgDir, "secrets", "rqlite-password")},
	}
	for _, c := range candidates {
		u, err := os.ReadFile(c.userFile)
		if err != nil {
			continue
		}
		p, err := os.ReadFile(c.passFile)
		if err != nil {
			continue
		}
		return strings.TrimSpace(string(u)), strings.TrimSpace(string(p))
	}
	return "", ""
}

// readRQLitePortFromConfig is a tiny YAML peek for `database.rqlite_port`.
// Avoids pulling the whole config loader; failure returns 0 → fallback used.
func readRQLitePortFromConfig(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "rqlite_port:") {
			continue
		}
		var port int
		_, err := fmt.Sscanf(line, "rqlite_port: %d", &port)
		if err == nil {
			return port
		}
	}
	return 0
}

func init() {
	schemaCmd.PersistentFlags().StringVar(&schemaDSN, "dsn", "",
		fmt.Sprintf("RQLite DSN (default: discover from node config or %s)", constants.LocalRQLiteURL()))
	schemaApplyCmd.Flags().BoolVar(&schemaYes, "yes", false,
		"Skip the confirmation prompt")

	schemaCmd.AddCommand(schemaStatusCmd)
	schemaCmd.AddCommand(schemaApplyCmd)
}
