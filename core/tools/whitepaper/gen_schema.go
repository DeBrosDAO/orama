package main

import (
	"context"
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
)

// placementSource holds the table classification map Appendix B reports.
const placementSource = "core/pkg/rqlite/schema_placement.go"

type tablePlacementNote struct{ placement, trust, why string }

type column struct {
	name, typ, dflt string
	notNull, pk     bool
}

// genSchema renders Appendix B: the schema every node's migrations produce,
// with each table's placement (registry or namespace) and trust class.
func genSchema(b *Book) ([]byte, error) {
	notes, err := readPlacement(filepath.Join(b.Root, placementSource))
	if err != nil {
		return nil, err
	}
	db, err := migratedDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	tables, err := tableNames(db)
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	sb.WriteString(generatedHeader(titleOf(b, "appendices/b-schema.md"), "`core/migrations/` applied to an empty database, joined with `"+placementSource+":tablePlacement`"))
	sb.WriteString("Placement says which database holds a table: `Cluster` tables live only in the cluster registry (the index RQLite); `Namespace` tables live in each namespace's own RQLite. Chapter 7 explains the split.\n\n")
	sb.WriteString("| Table | Placement | Trust | Why |\n|---|---|---|---|\n")
	for _, t := range tables {
		n := notes[t]
		fmt.Fprintf(&sb, "| `%s` | %s | %s | %s |\n", t, n.placement, n.trust, tableCell(n.why))
	}
	for _, t := range tables {
		if err := writeTable(&sb, db, t); err != nil {
			return nil, err
		}
	}
	return []byte(sb.String()), nil
}

func migratedDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("failed to open an in-memory SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := rqlite.ApplyEmbeddedMigrations(context.Background(), db, migrations.FS, zap.NewNop()); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to apply core/migrations to an empty database: %w", err)
	}
	return db, nil
}

func tableNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("failed to read a table name: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func writeTable(sb *strings.Builder, db *sql.DB, table string) error {
	cols, err := columns(db, table)
	if err != nil {
		return err
	}
	fmt.Fprintf(sb, "\n## %s\n\n| Column | Type | Not null | Default | Key |\n|---|---|---|---|---|\n", table)
	for _, c := range cols {
		fmt.Fprintf(sb, "| `%s` | %s | %s | %s | %s |\n", c.name, orDash(c.typ), yesNo(c.notNull), codeOrDash(c.dflt), map[bool]string{true: "primary", false: ""}[c.pk])
	}
	return nil
}

func columns(db *sql.DB, table string) ([]column, error) {
	rows, err := db.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, ''), pk FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("failed to read the columns of %s: %w", table, err)
	}
	defer rows.Close()
	var out []column
	for rows.Next() {
		var c column
		var notNull, pk int
		if err := rows.Scan(&c.name, &c.typ, &notNull, &c.dflt, &pk); err != nil {
			return nil, fmt.Errorf("failed to read a column of %s: %w", table, err)
		}
		c.notNull, c.pk = notNull == 1, pk > 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// readPlacement parses the tablePlacement map literal: each entry is
// "table": {PlacementX, TrustY, "why"}.
func readPlacement(path string) (map[string]tablePlacementNote, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	out := map[string]tablePlacementNote{}
	ast.Inspect(f, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		key, ok1 := kv.Key.(*ast.BasicLit)
		val, ok2 := kv.Value.(*ast.CompositeLit)
		if !ok1 || !ok2 || key.Kind != token.STRING || len(val.Elts) != 3 {
			return true
		}
		name, _ := strconv.Unquote(key.Value)
		why := ""
		if lit, ok := val.Elts[2].(*ast.BasicLit); ok {
			why, _ = strconv.Unquote(lit.Value)
		}
		out[name] = tablePlacementNote{
			placement: strings.TrimPrefix(identName(val.Elts[0]), "Placement"),
			trust:     strings.TrimPrefix(identName(val.Elts[1]), "Trust"),
			why:       why,
		}
		return true
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("found no table entries in %s: has tablePlacement changed shape?", path)
	}
	return out, nil
}

func identName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return "?"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func codeOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return "`" + strings.ReplaceAll(s, "|", "\\|") + "`"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
