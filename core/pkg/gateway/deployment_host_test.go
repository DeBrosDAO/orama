package gateway

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
	_ "github.com/mattn/go-sqlite3"
)

const hostTestBase = "orama.test"

// sqliteHostDB is a client.DatabaseClient over a real SQLite database, so the
// lookup's SQL is judged by an engine rather than matched against strings.
type sqliteHostDB struct {
	client.DatabaseClient
	db *sql.DB
}

func (s *sqliteHostDB) Query(ctx context.Context, query string, args ...interface{}) (*client.QueryResult, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := &client.QueryResult{Columns: cols}
	for rows.Next() {
		row := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range row {
			ptrs[i] = &row[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out.Rows = append(out.Rows, row)
	}
	return out, rows.Err()
}

type hostRow struct {
	id, namespace, name, subdomain, status string
}

func newHostDB(t *testing.T, rows ...hostRow) *sqliteHostDB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	for _, ddl := range []string{
		`CREATE TABLE deployments (id TEXT, namespace TEXT, name TEXT, type TEXT, port INTEGER,
			content_cid TEXT, status TEXT, home_node_id TEXT, subdomain TEXT)`,
		`CREATE TABLE deployment_domains (domain TEXT, deployment_id TEXT, verified_at TEXT)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range rows {
		var subdomain interface{} = r.subdomain
		if r.subdomain == "-" {
			subdomain = nil
		}
		if _, err := db.Exec(`INSERT INTO deployments VALUES (?, ?, ?, 'nodejs-backend', 10001, 'cid', ?, 'node-1', ?)`,
			r.id, r.namespace, r.name, r.status, subdomain); err != nil {
			t.Fatal(err)
		}
	}
	return &sqliteHostDB{db: db}
}

func lookup(t *testing.T, db *sqliteHostDB, host string) (string, error) {
	t.Helper()
	d, err := lookupDeploymentByHost(context.Background(), db, hostTestBase, host)
	if err != nil || d == nil {
		return "", err
	}
	return d.ID, nil
}

func TestLookupDeploymentByHost_theIssuedSubdomainResolves(t *testing.T) {
	db := newHostDB(t, hostRow{"d1", "acme", "api", "api-abc123", "active"})
	if id, err := lookup(t, db, "api-abc123."+hostTestBase); err != nil || id != "d1" {
		t.Fatalf("got %q, %v; want d1", id, err)
	}
}

func TestLookupDeploymentByHost_aLegacyDeploymentResolvesByItsBareName(t *testing.T) {
	for _, subdomain := range []string{"-", ""} {
		db := newHostDB(t, hostRow{"d1", "acme", "oldapp", subdomain, "active"})
		if id, err := lookup(t, db, "oldapp."+hostTestBase+"."); err != nil || id != "d1" {
			t.Errorf("subdomain %q: got %q, %v; want d1", subdomain, id, err)
		}
	}
}

// The squat: names are unique per namespace, so two namespaces can both own
// "login". Its bare host used to go to whichever row the database returned
// first.
func TestLookupDeploymentByHost_refusesALegacyNameTwoNamespacesShare(t *testing.T) {
	db := newHostDB(t,
		hostRow{"d1", "acme", "login", "-", "active"},
		hostRow{"d2", "mallory", "login", "-", "active"})
	id, err := lookup(t, db, "login."+hostTestBase)
	if !errors.Is(err, ErrAmbiguousLegacyHost) {
		t.Fatalf("got %q, %v; want ErrAmbiguousLegacyHost", id, err)
	}
	if id != "" {
		t.Fatalf("an ambiguous host resolved to %q", id)
	}
}

// A deployment that has a subdomain is reached through it. Its bare name was
// never published, so deploying a name must not capture the bare host of a
// legacy deployment of the same name, nor serve under it.
func TestLookupDeploymentByHost_aSubdomainedDeploymentIsNotReachedByItsBareName(t *testing.T) {
	db := newHostDB(t, hostRow{"d2", "mallory", "login", "login-zzz999", "active"})
	if id, err := lookup(t, db, "login."+hostTestBase); err != nil || id != "" {
		t.Fatalf("got %q, %v; want no deployment", id, err)
	}

	db = newHostDB(t,
		hostRow{"d1", "acme", "login", "-", "active"},
		hostRow{"d2", "mallory", "login", "login-zzz999", "active"})
	if id, err := lookup(t, db, "login."+hostTestBase); err != nil || id != "d1" {
		t.Fatalf("got %q, %v; want the legacy deployment d1", id, err)
	}
}

func TestLookupDeploymentByHost_onlyServingDeploymentsCountForAmbiguity(t *testing.T) {
	db := newHostDB(t,
		hostRow{"d1", "acme", "login", "-", "active"},
		hostRow{"d2", "mallory", "login", "-", "stopped"})
	if id, err := lookup(t, db, "login."+hostTestBase); err != nil || id != "d1" {
		t.Fatalf("got %q, %v; want d1", id, err)
	}
}

func TestLookupDeploymentByHost_unknownAndForeignHosts(t *testing.T) {
	db := newHostDB(t, hostRow{"d1", "acme", "api", "api-abc123", "active"})
	for _, host := range []string{
		"nothing." + hostTestBase,
		"a.b." + hostTestBase,
		hostTestBase,
		"api-abc123.other.test",
	} {
		if id, err := lookup(t, db, host); err != nil || id != "" {
			t.Errorf("%s: got %q, %v; want no deployment", host, id, err)
		}
	}
}

func TestLookupDeploymentByHost_aVerifiedCustomDomainResolves(t *testing.T) {
	db := newHostDB(t, hostRow{"d1", "acme", "api", "api-abc123", "active"})
	if _, err := db.db.Exec(`INSERT INTO deployment_domains VALUES ('shop.example.com', 'd1', '2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO deployment_domains VALUES ('pending.example.com', 'd1', NULL)`); err != nil {
		t.Fatal(err)
	}
	if id, err := lookup(t, db, "shop.example.com"); err != nil || id != "d1" {
		t.Fatalf("got %q, %v; want d1", id, err)
	}
	if id, err := lookup(t, db, "pending.example.com"); err != nil || id != "" {
		t.Fatalf("an unverified domain resolved: %q, %v", id, err)
	}
}

// A database failure is reported, not read as "no such host".
func TestLookupDeploymentByHost_aFailedQueryIsAnError(t *testing.T) {
	db := newHostDB(t)
	if _, err := db.db.Exec(`DROP TABLE deployments`); err != nil {
		t.Fatal(err)
	}
	if _, err := lookup(t, db, "api."+hostTestBase); err == nil {
		t.Fatal("a failing query was read as an unknown host")
	}
}
