package gateway

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/sqlguard"
)

// sqlVerbs open the statements the scan below collects.
var sqlVerbs = []string{"SELECT", "INSERT", "UPDATE", "DELETE", "REPLACE", "WITH"}

// familySources are where the deployment family's statements are written: the
// packages whose database is the handle scopedDeploymentRegistry returns.
var familySources = []string{
	"../deployments",
	"handlers/deployments",
	"workload_token.go",
}

func goFilesUnder(t *testing.T, path string) []string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.IsDir() {
		return []string{path}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(path, name))
	}
	return out
}

// statementsIn returns every string literal in a Go file that opens like a SQL
// statement.
func statementsIn(t *testing.T, file string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var out []string
	ast.Inspect(parsed, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		upper := strings.ToUpper(strings.TrimSpace(text))
		for _, verb := range sqlVerbs {
			if strings.HasPrefix(upper, verb+" ") || strings.HasPrefix(upper, verb+"\n") || strings.HasPrefix(upper, verb+"\t") {
				out = append(out, text)
				return true
			}
		}
		return true
	})
	return out
}

// The scope is only as good as its list. A statement the family issues against
// a table the list lacks would be refused on every namespace gateway, so this
// reads the statements out of the source and runs each through the guard.
func TestDeploymentRegistryTables_coverEveryStatementTheFamilyIssues(t *testing.T) {
	guard := deploymentRegistryGuard()
	checked := 0
	for _, source := range familySources {
		for _, file := range goFilesUnder(t, source) {
			for _, stmt := range statementsIn(t, file) {
				checked++
				if err := guard(stmt); err != nil {
					t.Errorf("%s: %v\n  statement: %s", file, err, strings.Join(strings.Fields(stmt), " "))
				}
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d statements found in %v: the scan is not reading the family's source", checked, familySources)
	}
}

func TestDeploymentRegistryTables_areRegistryTables(t *testing.T) {
	known := rqlite.KnownTables()
	for _, table := range deploymentRegistryTables {
		if !known[table] {
			t.Errorf("%s is in the deployment scope but is not a table the migrations create", table)
		}
	}
}

func TestDeploymentRegistryGuard_refusesTablesOutsideTheFamily(t *testing.T) {
	guard := deploymentRegistryGuard()
	for _, stmt := range []string{
		"SELECT * FROM api_keys",
		"SELECT ikm FROM encryption_roots",
		"UPDATE wireguard_peers SET agent_token = ''",
		"SELECT id FROM deployments d JOIN principals p ON p.id = d.id",
		"DELETE FROM grants",
		"SELECT * FROM sqlite_master",
		"DROP TABLE deployments",
		"SELECT 1; SELECT * FROM deployments",
	} {
		err := guard(stmt)
		var refused *sqlguard.ErrNotAllowed
		if !errors.As(err, &refused) {
			t.Errorf("%q was not refused: %v", stmt, err)
		}
	}
}

func TestDeploymentRegistryGuard_allowsTheFamilysOwnTables(t *testing.T) {
	guard := deploymentRegistryGuard()
	for _, stmt := range []string{
		"SELECT * FROM deployments WHERE namespace = ? AND name = ? LIMIT 1",
		"INSERT INTO deployment_replicas (deployment_id, node_id) VALUES (?, ?)",
		"DELETE FROM dns_records WHERE fqdn = ? AND deployment_id = ?;",
		"UPDATE port_allocations SET deployment_id = ? WHERE node_id = ?",
	} {
		if err := guard(stmt); err != nil {
			t.Errorf("%q was refused: %v", stmt, err)
		}
	}
}

func TestScopedDeploymentRegistry_onlyANamespaceGatewayIsScoped(t *testing.T) {
	tenant, registry := rqlite.NewClient(nil), rqlite.NewClient(nil)
	ns := &Config{RQLiteDSN: "http://10.0.0.1:10035", GlobalRQLiteDSN: "http://10.0.0.1:10100"}
	if _, ok := scopedDeploymentRegistry(ns, &Dependencies{ORMClient: tenant, GlobalORMClient: registry}).(*rqlite.GuardedClient); !ok {
		t.Error("a namespace gateway's deployment family holds the whole registry")
	}

	index := &Config{RQLiteDSN: "http://10.0.0.1:10100"}
	if got := scopedDeploymentRegistry(index, &Dependencies{ORMClient: registry, GlobalORMClient: registry}); got != registry {
		t.Error("the index gateway's deployment family is not the registry itself")
	}

	if got := scopedDeploymentRegistry(ns, &Dependencies{}); got != nil {
		t.Errorf("with no registry the scoped handle is %v, want nil so the family stays off", got)
	}
}
