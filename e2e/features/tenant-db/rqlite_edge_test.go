//go:build e2e_fleet

package tenantdb

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	pollEvery = 2 * time.Second
	// operatorBudget covers the gateways' operator cache after
	// `orama operator add`.
	operatorBudget = time.Minute
)

// TestRQLiteInput_argsAreNeverSQL: values travel as bound arguments, so SQL in
// a value is stored as text and executes nothing.
func TestRQLiteInput_argsAreNeverSQL(t *testing.T) {
	t.Parallel()
	d := ownerDB(t, tenancy.Namespace(t, harness.Fleet(t), ns.Options{}))
	d.people()
	hostile := []string{"x'); DROP TABLE people; --", `" OR 1=1 --`, "\u202eevil\u0000nul", "名前 ﷽ é", strings.Repeat("z", 64<<10)}
	for _, v := range hostile {
		d.exec(`INSERT INTO people (name, age) VALUES (?, ?)`, v, 1).Expect(t, http.StatusOK)
		got := d.query(`SELECT name FROM people WHERE name = ?`, v)
		if got.Count != 1 || got.Items[0]["name"] != v {
			t.Errorf("value %.40q did not round-trip: %+v", v, got.Items)
		}
	}
	if got := d.query(`SELECT COUNT(*) AS n FROM people`); got.Items[0]["n"] != float64(3+len(hostile)) {
		t.Fatalf("hostile values changed more than their own rows: %v", got.Items)
	}
}

// TestRQLiteInput_identifiersAreNotInterpolated: drop-table accepts only a
// plain identifier (core/pkg/rqlite/gateway.go identRe); find's criteria
// keys must not widen a query into the whole table.
func TestRQLiteInput_identifiersAreNotInterpolated(t *testing.T) {
	t.Parallel()
	d := ownerDB(t, tenancy.Namespace(t, harness.Fleet(t), ns.Options{}))
	d.people()
	for _, name := range []string{"people; DROP TABLE people", "people--", "\"people\"", "../people", ""} {
		if r := d.call(pathDropTable, map[string]any{"table": name}); r.Status != http.StatusBadRequest {
			t.Errorf("drop-table %q: want 400, got %d", name, r.Status)
		}
	}
	r := d.call(pathFind, map[string]any{"table": "people", "criteria": map[string]any{"name = name OR 1": 1}})
	if r.Status == http.StatusOK {
		var got rows
		if err := r.Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Count >= 3 {
			t.Fatalf("a criteria key was executed as SQL and returned the whole table: %d rows", got.Count)
		}
	}
	if got := d.query(`SELECT COUNT(*) AS n FROM people`); got.Items[0]["n"] != 3.0 {
		t.Fatal("identifier attacks changed the table")
	}
}

// TestRQLiteInput_constraintAndSyntaxErrorsChangeNothing.
func TestRQLiteInput_constraintAndSyntaxErrorsChangeNothing(t *testing.T) {
	t.Parallel()
	d := ownerDB(t, tenancy.Namespace(t, harness.Fleet(t), ns.Options{}))
	d.people()
	for name, r := range map[string]*gw.Response{
		"unique":      d.exec(`INSERT INTO people (name, age) VALUES (?, ?)`, "ada", 1),
		"not null":    d.exec(`INSERT INTO people (age) VALUES (?)`, 1),
		"syntax":      d.exec(`INSERT INTO people VALUES (`),
		"no table":    d.exec(`INSERT INTO nope (x) VALUES (1)`),
		"arg missing": d.exec(`INSERT INTO people (name, age) VALUES (?, ?)`, "solo"),
	} {
		if r.Status < 400 || r.Status == http.StatusBadGateway {
			t.Errorf("%s: want a refusal, got %d: %s", name, r.Status, r.Body)
		}
	}
	// Creating a table that exists is refused; which refusal is not pinned
	// (nothing documents it, and today's 500 is not a status to keep).
	if r := d.call(pathCreateTable, map[string]any{"schema": peopleDDL}); r.Status < 400 || r.Status == http.StatusBadGateway {
		t.Errorf("re-creating a table that exists: want a refusal, got %d: %s", r.Status, r.Body)
	}
	if got := d.query(`SELECT COUNT(*) AS n FROM people`); got.Items[0]["n"] != 3.0 {
		t.Fatalf("failed statements changed the table: %v", got.Items)
	}
}

func TestRQLiteInput_malformedAndOversized(t *testing.T) {
	t.Parallel()
	d := ownerDB(t, tenancy.Namespace(t, harness.Fleet(t), ns.Options{}))
	for name, body := range map[string][]byte{
		"not json": []byte("sql=SELECT 1"), "no sql": []byte(`{"args":[]}`),
		"blank sql": []byte(`{"sql":"   "}`), "sql not text": []byte(`{"sql":1}`),
	} {
		for _, path := range []string{pathQuery, pathExec} {
			if r := d.call(path, body); r.Status != http.StatusBadRequest {
				t.Errorf("%s %s: want 400, got %d", path, name, r.Status)
			}
		}
	}
	for _, path := range []string{pathFind, pathFindOne, pathSelect} {
		d.call(path, map[string]any{"criteria": map[string]any{}}).Expect(t, http.StatusBadRequest)
	}
	d.call(pathCreateTable, map[string]any{"schema": ""}).Expect(t, http.StatusBadRequest)
	for _, path := range []string{pathQuery, pathExec, pathTx, pathCreateTable} {
		tenancy.Send(t, d.c, http.MethodGet, path, d.who, nil).Expect(t, http.StatusMethodNotAllowed)
	}
	tenancy.Send(t, d.c, http.MethodPost, pathSchema, d.who, nil).Expect(t, http.StatusMethodNotAllowed)
	// An 8 MiB statement is refused or fails; it never takes the gateway down.
	huge := "SELECT '" + strings.Repeat("a", 8<<20) + "'"
	if r := d.call(pathQuery, map[string]any{"sql": huge}); r.Status == http.StatusOK || r.Status == http.StatusBadGateway {
		t.Errorf("an 8 MiB statement answered %d", r.Status)
	}
	d.query(`SELECT 1`)
}

// TestRQLiteAuth_rolesDecide: the raw database is the admin grant's
// (docs/CLI_REFERENCE.md "orama members": admin is the control plane,
// including the raw database; runtime is the data plane).
func TestRQLiteAuth_rolesDecide(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	body := map[string]any{"sql": "SELECT 1", "args": []any{}}
	tenancy.Post(t, n.Client, pathQuery, tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleAdmin).Token()}, body).Expect(t, http.StatusOK)
	tenancy.ExpectRefused(t, tenancy.Post(t, n.Client, pathQuery, tenancy.Cred{}, body), http.StatusUnauthorized, tenancy.CodeMissing)
	for name, who := range map[string]tenancy.Cred{
		"runtime member": {Bearer: tenancy.Member(t, n, tenancy.RoleRuntime).Token()},
		"reader member":  {Bearer: tenancy.Member(t, n, tenancy.RoleReader).Token()},
		"cache key":      {APIKey: tenancy.APIKey(t, n, "cache")},
	} {
		for _, path := range []string{pathQuery, pathExec, pathTx, pathCreateTable} {
			if r := tenancy.Post(t, n.Client, path, who, body); r.Status != http.StatusForbidden || r.ErrorCode() != tenancy.CodeScope {
				t.Errorf("%s on %s: want 403 %s, got %d %s", name, path, tenancy.CodeScope, r.Status, r.ErrorCode())
			}
		}
	}
}

// TestRQLiteIsolation_otherNamespaceRefused: B's credentials never reach A's
// database, and each sees only its own tables.
func TestRQLiteIsolation_otherNamespaceRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	pair := tenancy.Namespaces(t, f, 2, ns.Options{})
	a, b := pair[0], pair[1]
	ownerDB(t, a).people()
	body := map[string]any{"sql": "SELECT * FROM people", "args": []any{}}
	tenancy.ExpectDenied(t, tenancy.Post(t, a.Client, pathQuery, tenancy.Owner(b), body), "B's session at A's database")
	tenancy.ExpectRefused(t, tenancy.Post(t, a.Client, pathQuery, tenancy.Cred{APIKey: tenancy.APIKey(t, b, "admin")}, body), http.StatusForbidden, tenancy.CodeMismatch)
	if r := tenancy.Post(t, b.Client, pathQuery, tenancy.Owner(b), body); r.Status == http.StatusOK {
		t.Fatalf("B's database has A's table: %s", r.Body)
	}
}

// TestRQLiteIsolation_clusterRegistryNeedsOperator: the cluster gateway's
// database is the registry; a tenant is refused NOT_AN_OPERATOR and pointed at
// its namespace gateway, and the same request from an operator is served, so
// the refusal is the operator check and not a route that serves nobody
// (core/pkg/gateway/core_registry_guard.go).
func TestRQLiteIsolation_clusterRegistryNeedsOperator(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	c := harness.GW(t)
	body := []byte(`{"sql":"SELECT COUNT(*) AS n FROM operators"}`)
	lobby := gw.NewUser(t, f, gw.LobbyNamespace)
	tenancy.ExpectRefused(t, tenancy.Post(t, c, pathQuery, tenancy.Cred{Bearer: lobby.Token()}, body), http.StatusForbidden, tenancy.CodeNotOperator)
	// An operator reaches the registry with a credential of a namespace it
	// administers: the lobby belongs to nobody and its sessions hold no
	// permission, so a lobby sign-in is never served a db route
	// (docs/AUTH.md "The lobby"), operator or not.
	op := tenancy.Member(t, tenancy.Namespace(t, f, ns.Options{}), tenancy.RoleAdmin)
	tenancy.ExpectRefused(t, tenancy.Post(t, c, pathQuery, tenancy.Cred{Bearer: op.Token()}, body), http.StatusForbidden, tenancy.CodeNotOperator)
	cli := harness.CLI(t)
	cli.MustOK(t, "operator", "add", op.Wallet.Address())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		if res, err := cli.Run(ctx, "operator", "remove", op.Wallet.Address()); err != nil || res.Exit != 0 {
			t.Errorf("cleanup: the throwaway operator %s was not removed: %v %s", op.Wallet.Address(), err, res.Stderr)
		}
	})
	eventually.Require(t, pollEvery, operatorBudget, "an operator to read the registry", func() (bool, error) {
		r, err := c.Send(t.Context(), gw.Req{Method: http.MethodPost, Path: pathQuery, Bearer: op.Token(),
			Header: http.Header{"Content-Type": {"application/json"}}, Body: body})
		if err != nil {
			return false, err
		}
		if r.Status != http.StatusOK {
			return false, fmt.Errorf("HTTP %d %s", r.Status, r.ErrorCode())
		}
		return true, nil
	})
}

// TestRQLiteIsolation_platformTablesUnreachable: the namespace database also
// holds the tables that authenticate it (docs/SECURITY.md "Function SQL":
// api_keys, grants, refresh_tokens, ...). A member with the admin grant is not
// the owner, and must not read credentials or rewrite grants with raw SQL:
// ownership moves only by transfer (docs/CLI_REFERENCE.md "orama members
// transfer").
func TestRQLiteIsolation_platformTablesUnreachable(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	admin := &db{t: t, n: n, who: tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleAdmin).Token()}, c: n.Client}
	for _, sql := range []string{"SELECT * FROM api_keys", "SELECT * FROM refresh_tokens", "SELECT * FROM grants",
		"SELECT * FROM function_secrets", `SELECT * FROM "api_keys"`, "SELECT * FROM main.grants"} {
		if r := admin.call(pathQuery, map[string]any{"sql": sql}); r.Status == http.StatusOK {
			t.Errorf("an admin member read a platform table with %q", sql)
		}
	}
	admin.exec(`UPDATE grants SET role = 'owner' WHERE role = 'admin'`)
	var members struct {
		Members []struct{ Role, Identifier string } `json:"members"`
	}
	if err := tenancy.Send(t, n.Client, http.MethodGet, tenancy.PathMembers, tenancy.Owner(n), nil).Expect(t, http.StatusOK).Decode(&members); err != nil {
		t.Fatal(err)
	}
	owners := 0
	for _, m := range members.Members {
		if m.Role == "owner" {
			owners++
			if !strings.EqualFold(m.Identifier, n.Owner.Wallet.Address()) {
				t.Errorf("the namespace is now owned by %s", m.Identifier)
			}
		}
	}
	if owners != 1 {
		t.Errorf("the namespace has %d owners after an admin's raw UPDATE of grants", owners)
	}
}

// TestRQLiteIsolation_platformTablesRefusedToEveryRole: the raw-database routes
// run the same SQL filter as a function (docs/SECURITY.md "Function SQL"). A
// developer holds db:write and nothing that manages members, and must not mint
// authority or claim another tenant's content by writing the platform tables
// directly — through exec, inside a transaction, or through the builders. The
// owner and an admin are refused too: writing api_keys directly bypasses the
// key-minting path. The tenant's own tables are untouched.
func TestRQLiteIsolation_platformTablesRefusedToEveryRole(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	callers := map[string]tenancy.Cred{
		"owner":     tenancy.Owner(n),
		"admin":     {Bearer: tenancy.Member(t, n, tenancy.RoleAdmin).Token()},
		"developer": {Bearer: tenancy.Member(t, n, tenancy.RoleDeveloper).Token()},
	}
	refused := map[string]map[string]any{
		pathExec:  {"sql": "INSERT INTO ipfs_content_ownership (cid, namespace, uploaded_by) VALUES ('QmVictim', 'x', 'x')"},
		pathQuery: {"sql": `SELECT * FROM "api_keys"`},
		pathTx: {"ops": []map[string]any{
			{"kind": "exec", "sql": "CREATE TABLE IF NOT EXISTS guard_ok (id INTEGER)"},
			{"kind": "exec", "sql": "UPDATE grants SET role = 'owner'"},
		}},
		pathSelect:      {"table": "grants"},
		pathFind:        {"table": "deployments"},
		pathCreateTable: {"schema": "CREATE TABLE api_keys (id INTEGER)"},
	}
	for who, cred := range callers {
		d := &db{t: t, n: n, who: cred, c: n.Client}
		for path, body := range refused {
			r := d.call(path, body)
			if r.Status != http.StatusForbidden || r.ErrorCode() != "SQL_NOT_ALLOWED" {
				t.Errorf("%s %s: HTTP %d %s, want 403 SQL_NOT_ALLOWED", who, path, r.Status, r.ErrorCode())
			}
		}
	}
	dev := &db{t: t, n: n, who: callers["developer"], c: n.Client}
	if r := dev.call(pathCreateTable, map[string]any{"schema": "CREATE TABLE guard_ok (id INTEGER PRIMARY KEY, v TEXT)"}); r.Status >= 300 {
		t.Fatalf("a developer's own table was refused: HTTP %d %s", r.Status, r.ErrorCode())
	}
	dev.exec("INSERT INTO guard_ok (v) VALUES (?)", "grants").Expect(t, http.StatusOK)
	if got := dev.query("SELECT v FROM guard_ok"); got.Count != 1 {
		t.Errorf("the developer's own row is missing: %v", got)
	}
}

// TestRQLiteIsolation_triggerAndHistoryTablesRefusedToADeveloper: a cron or
// pubsub firing skips the caller check (docs/SECURITY.md), so a db:write member
// who could insert a trigger row could run a private function, and a forged
// deployment_history row picks the CID a rollback restores. The developer role
// holds db:write and nothing else; both are refused through exec and inside a
// transaction, and so is the `functions` table itself.
func TestRQLiteIsolation_triggerAndHistoryTablesRefusedToADeveloper(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	dev := &db{t: t, n: n, who: tenancy.Cred{Bearer: tenancy.Member(t, n, tenancy.RoleDeveloper).Token()}, c: n.Client}
	for _, sql := range []string{
		"INSERT INTO function_cron_triggers (id, function_id, cron_expression) VALUES ('forged', 'f', '* * * * *')",
		"INSERT INTO function_pubsub_triggers (id, function_id, topic) VALUES ('forged', 'f', 'x')",
		"INSERT INTO deployment_history (id, deployment_id, version, content_cid) VALUES ('forged', 'd', 1, 'QmForged')",
		"UPDATE functions SET is_public = 1",
	} {
		refused := map[string]map[string]any{
			pathExec: {"sql": sql},
			pathTx: {"ops": []map[string]any{
				{"kind": "exec", "sql": "CREATE TABLE IF NOT EXISTS guard_ok (id INTEGER)"},
				{"kind": "exec", "sql": sql},
			}},
		}
		for path, body := range refused {
			r := dev.call(path, body)
			if r.Status != http.StatusForbidden || r.ErrorCode() != "SQL_NOT_ALLOWED" {
				t.Errorf("%s %q: HTTP %d %s, want 403 SQL_NOT_ALLOWED", path, sql, r.Status, r.ErrorCode())
			}
		}
	}
}

func TestSchemaStatus_inSyncOnEveryGateway(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	for _, nc := range tenancy.PerNode(t, f, n.Client) {
		var st struct {
			OK       bool `json:"ok"`
			InSync   bool `json:"in_sync"`
			Required int  `json:"required_version"`
			Applied  int  `json:"applied_version"`
		}
		if err := tenancy.Send(t, nc.Client, http.MethodGet, pathSchemaState, tenancy.Owner(n), nil).Expect(t, http.StatusOK).Decode(&st); err != nil {
			t.Fatal(err)
		}
		if !st.InSync || st.Applied != st.Required || st.Required == 0 {
			t.Errorf("%s: schema status %+v", nc.Node.Name, st)
		}
	}
	tenancy.ExpectRefused(t, tenancy.Send(t, n.Client, http.MethodGet, pathSchemaState, tenancy.Cred{}, nil), http.StatusUnauthorized, tenancy.CodeMissing)
	tenancy.Send(t, n.Client, http.MethodPost, pathSchemaState, tenancy.Owner(n), nil).Expect(t, http.StatusMethodNotAllowed)
}
