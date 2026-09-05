package hostfunctions

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/migrations"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// refusingDB is a database handle that panics if anything reaches it. The
// guard's job is that nothing does.
type refusingDB struct{ rqlite.Client }

// The escalation this guard exists to stop. A namespace database is both the
// tenant's application database and the database that authenticates the
// namespace, so a function's SQL could mint itself a key, make a wallet the
// namespace's owner, or read every other function's secrets.
func TestCheckGuestSQL_refusesTheTablesThatGrantAuthority(t *testing.T) {
	for _, query := range []string{
		"SELECT key, scopes FROM api_keys",
		"UPDATE api_keys SET scopes = 'admin' WHERE id = 1",
		"INSERT INTO grants(principal_id, namespace_id, role) VALUES (1, 1, 'owner')",
		"DELETE FROM grants",
		"UPDATE grants SET role = 'owner' WHERE id = 1",
		"INSERT INTO principals(type, identifier) VALUES ('wallet', '0xattacker')",
		"SELECT * FROM function_secrets",
		"SELECT value FROM function_env_vars",
		"SELECT token FROM refresh_tokens",
		"SELECT * FROM nonces",
		"INSERT INTO operators(wallet) VALUES ('0xattacker')",
		"SELECT agent_token FROM wireguard_peers",
		"SELECT * FROM invite_tokens",
		"UPDATE namespace_rate_limit_config SET requests_per_minute = 1000000",
		"UPDATE namespace_quotas SET storage_bytes = 0",
		"INSERT INTO dns_records(name, value) VALUES ('x', 'y')",
		"SELECT * FROM orama_schema_migrations",
	} {
		t.Run(query, func(t *testing.T) {
			if err := checkGuestSQL(query); err == nil {
				t.Errorf("allowed: %s", query)
			}
		})
	}
}

// Every way SQLite lets a name be written has to reach the same answer, or the
// guard is a spelling test.
func TestCheckGuestSQL_refusesEveryWayOfWritingTheName(t *testing.T) {
	for _, query := range []string{
		`SELECT * FROM api_keys`,
		`SELECT * FROM API_KEYS`,
		`SELECT * FROM Api_Keys`,
		`SELECT * FROM "api_keys"`,
		"SELECT * FROM `api_keys`",
		`SELECT * FROM [api_keys]`,
		`SELECT * FROM 'api_keys'`,
		`SELECT * FROM main.api_keys`,
		`SELECT * FROM "main"."api_keys"`,
		`SELECT * FROM main.'api_keys'`,
		`SELECT * FROM /* hide */ api_keys`,
		"SELECT * -- comment\nFROM api_keys",
		`SELECT * FROM   api_keys`,
		`WITH leak AS (SELECT * FROM api_keys) SELECT * FROM leak`,
		`SELECT (SELECT key FROM api_keys LIMIT 1)`,
		`SELECT * FROM messages WHERE id IN (SELECT id FROM api_keys)`,
		`CREATE VIEW leak AS SELECT * FROM api_keys`,
		`CREATE TRIGGER t AFTER INSERT ON api_keys BEGIN SELECT 1; END`,
		`INSERT INTO messages SELECT * FROM api_keys`,
		`SELECT * FROM messages JOIN api_keys ON 1=1`,
		`ALTER TABLE api_keys RENAME TO mine`,
		`DROP TABLE api_keys`,
	} {
		t.Run(query, func(t *testing.T) {
			if err := checkGuestSQL(query); err == nil {
				t.Errorf("allowed: %s", query)
			}
		})
	}
}

func TestCheckGuestSQL_allowsATenantsOwnSQL(t *testing.T) {
	for _, query := range []string{
		"SELECT * FROM messages WHERE conversation_id = ?",
		"INSERT INTO conversations(id, name) VALUES (?, ?)",
		"UPDATE users SET display_name = ? WHERE wallet = ?",
		"DELETE FROM attachments WHERE created_at < ?",
		"SELECT COUNT(*) AS n FROM friend_requests",
		"CREATE TABLE IF NOT EXISTS my_table (id TEXT PRIMARY KEY)",
		"SELECT * FROM messages ORDER BY created_at DESC LIMIT 50;",
		"WITH recent AS (SELECT * FROM messages LIMIT 10) SELECT * FROM recent",
		"SELECT * FROM apps",              // core owns the name, but a tenant may already be using it
		"SELECT * FROM deployments",       // likewise
		"SELECT * FROM schema_migrations", // the tenant's own migration tracker
	} {
		t.Run(query, func(t *testing.T) {
			if err := checkGuestSQL(query); err != nil {
				t.Errorf("refused ordinary tenant SQL %q: %v", query, err)
			}
		})
	}
}

// A protected name inside the tenant's own data is data, not a table
// reference. Refusing it would make the guard unusable for a chat app.
func TestCheckGuestSQL_aProtectedNameAsDataIsAllowed(t *testing.T) {
	for _, query := range []string{
		"INSERT INTO messages(body) VALUES ('api_keys')",
		"SELECT * FROM messages WHERE body = 'talk about api_keys'",
		"UPDATE users SET bio = 'I broke the nonces table once' WHERE id = ?",
	} {
		t.Run(query, func(t *testing.T) {
			if err := checkGuestSQL(query); err != nil {
				t.Errorf("refused a protected name used as data: %q: %v", query, err)
			}
		})
	}
}

// Comments are removed before anything is looked at, so a protected name
// mentioned in one is not a reason to refuse a statement that never touches it.
// A guard that refused these would be unusable on a codebase whose SQL is
// commented.
func TestCheckGuestSQL_aProtectedNameInACommentIsNotAReference(t *testing.T) {
	for _, query := range []string{
		"SELECT * FROM messages -- api_keys lives elsewhere",
		"SELECT * FROM messages /* not api_keys */ WHERE id = ?",
		"/* function_secrets are read with get_secret */\nSELECT * FROM messages",
		"SELECT * FROM messages\n-- TODO: stop copying this into nonces\n",
	} {
		t.Run(query, func(t *testing.T) {
			if err := checkGuestSQL(query); err != nil {
				t.Errorf("refused a statement that only mentions a protected name in a comment: %q: %v", query, err)
			}
		})
	}
}

// A bracketed identifier is one name however it is spelled inside, so a table
// whose name contains a space does not split into pieces that happen to match
// something else.
func TestCheckGuestSQL_bracketQuotingIsOneIdentifier(t *testing.T) {
	if err := checkGuestSQL("SELECT * FROM [my table]"); err != nil {
		t.Errorf("refused a bracketed name with a space: %v", err)
	}
	if err := checkGuestSQL(`SELECT * FROM "my table"`); err != nil {
		t.Errorf("refused a double-quoted name with a space: %v", err)
	}
	if err := checkGuestSQL("SELECT * FROM [api_keys]"); err == nil {
		t.Error("a bracketed protected name was allowed")
	}

	// A quoted name that merely contains a protected word is a different
	// table. Reading the brackets is what tells the two apart; without them
	// the words inside are looked at one by one and this is refused.
	for _, query := range []string{
		"SELECT * FROM [my api_keys backup]",
		`SELECT * FROM "archived api_keys 2024"`,
		"SELECT * FROM `nonces of my own`",
	} {
		if err := checkGuestSQL(query); err != nil {
			t.Errorf("refused a distinct table whose quoted name contains a protected word: %q: %v", query, err)
		}
	}
}

func TestCheckGuestSQL_refusesStatementsThatLeaveTheDatabaseItWasGiven(t *testing.T) {
	for _, query := range []string{
		"ATTACH DATABASE '/opt/orama/.orama/data/other.db' AS other",
		"attach database 'x' as y",
		"DETACH DATABASE other",
		"PRAGMA table_info(messages)",
		"pragma journal_mode = WAL",
		"VACUUM INTO '/tmp/copy.db'",
	} {
		t.Run(query, func(t *testing.T) {
			if err := checkGuestSQL(query); err == nil {
				t.Errorf("allowed: %s", query)
			}
		})
	}
}

func TestCheckGuestSQL_refusesASecondStatement(t *testing.T) {
	for _, query := range []string{
		"SELECT 1; SELECT * FROM api_keys",
		"INSERT INTO messages(body) VALUES ('x'); DROP TABLE messages",
	} {
		t.Run(query, func(t *testing.T) {
			if err := checkGuestSQL(query); err == nil {
				t.Errorf("allowed two statements in one call: %s", query)
			}
		})
	}
	if err := checkGuestSQL("SELECT * FROM messages;"); err != nil {
		t.Errorf("a trailing semicolon was refused: %v", err)
	}
	if err := checkGuestSQL("SELECT * FROM messages;  \n "); err != nil {
		t.Errorf("a trailing semicolon with whitespace was refused: %v", err)
	}
}

func TestCheckGuestSQL_saysWhatIsWrong(t *testing.T) {
	err := checkGuestSQL("SELECT * FROM api_keys")
	if err == nil {
		t.Fatal("allowed")
	}
	if !strings.Contains(err.Error(), "api_keys") {
		t.Errorf("the refusal does not name the table: %v", err)
	}
	if !strings.Contains(err.Error(), "platform") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	err = checkGuestSQL("PRAGMA foreign_keys = ON")
	if err == nil {
		t.Fatal("allowed")
	}
	if !strings.Contains(err.Error(), "PRAGMA") {
		t.Errorf("the refusal does not name the statement: %v", err)
	}
}

func TestCheckGuestSQL_emptyAndTrivial(t *testing.T) {
	for _, query := range []string{"", "   ", "\n", ";"} {
		if err := checkGuestSQL(query); err != nil {
			t.Errorf("checkGuestSQL(%q) = %v, want nil", query, err)
		}
	}
}

// Every DB host function has to ask. One that does not is the whole hole
// reopened through a different name.
func TestDBHostFunctions_allRefuseAProtectedTable(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop(), db: refusingDB{}}
	ctx := context.Background()
	protected := "SELECT * FROM api_keys"

	if _, err := h.DBQuery(ctx, protected, nil); err == nil {
		t.Error("db_query ran it")
	}
	if _, err := h.DBExecute(ctx, protected, nil); err == nil {
		t.Error("db_execute ran it")
	}
	if _, err := h.DBExecuteV2(ctx, protected, nil); err == nil {
		t.Error("db_execute_v2 ran it")
	}
	if _, err := h.DBQueryV2(ctx, protected, nil); err == nil {
		t.Error("db_query_v2 ran it")
	}
	if _, err := h.DBTransaction(ctx, []byte(`{"ops":[{"kind":"exec","sql":"SELECT * FROM api_keys"}]}`)); err == nil {
		t.Error("db_transaction ran it")
	}
	if _, err := h.DBQueryBatch(ctx, []byte(`{"ops":[{"sql":"SELECT * FROM api_keys"}]}`)); err == nil {
		t.Error("db_query_batch ran it")
	}
}

// A protected table hidden behind an innocent first op still has to be caught.
func TestDBTransaction_checksEveryOp(t *testing.T) {
	h := &HostFunctions{logger: zap.NewNop(), db: refusingDB{}}
	_, err := h.DBTransaction(context.Background(),
		[]byte(`{"ops":[{"kind":"exec","sql":"INSERT INTO messages(body) VALUES ('x')"},{"kind":"exec","sql":"UPDATE api_keys SET scopes='admin'"}]}`))
	if err == nil {
		t.Fatal("a protected table in the second op ran")
	}
	if !strings.Contains(err.Error(), "op 1") {
		t.Errorf("the refusal does not say which op: %v", err)
	}
}

// Tables a function may name. Each is here because a tenant may already
// be using the name as their own — a namespace database has exactly one
// table called `apps`, and it belongs to whoever wrote to it first — or
// because reading it tells a tenant only about their own namespace.
//
// `audit_events` used to be on this list for the second reason. It moved to the
// registry, where it holds every namespace's trail, so the reason stopped being
// true.
var reachableTables = map[string]bool{
	"apps": true, "namespaces": true,
	"deployments": true, "deployment_domains": true, "deployment_events": true,
	"deployment_health_checks": true, "deployment_history": true,
	"deployment_replicas": true, "port_allocations": true,
	"home_node_assignments": true,
	"functions":             true, "function_invocations": true, "function_logs": true,
	"function_jobs": true, "function_timers": true, "function_rate_limits": true,
	"function_cron_triggers": true, "function_db_triggers": true,
	"function_pubsub_triggers": true, "function_db_change_tracking": true,
	"ipfs_content_ownership": true, "namespace_sqlite_backups": true,
	"namespace_sqlite_databases": true, "namespace_publish_seq": true,
	"namespace_webrtc_config": true, "namespace_push_config": true,
	"namespace_cluster_events": true, "namespace_pending_cleanup": true,
	"node_health_events": true, "request_logs": true, "rqlite_backups": true,
	"push_devices": true, "webrtc_rooms": true, "webrtc_port_allocations": true,
	"schema_migrations": true, "subscriptions": true,
}

// The protected list is a decision about each core table. A migration that adds
// one has to be triaged rather than defaulting to reachable, so this fails until
// somebody says which side the new table is on.
func TestProtectedTables_everyCoreTableIsTriaged(t *testing.T) {
	for table := range coreTables(t) {
		_, protected := protectedTables[table]
		if !protected && !reachableTables[table] {
			t.Errorf("the core migrations create %q and nothing here says whether a function may name it. "+
				"Add it to protectedTables if it grants authority, holds a credential or configures the "+
				"platform; add it to the reachable list here, with a reason, if it does not.", table)
		}
		if protected && reachableTables[table] {
			t.Errorf("%q is on both lists", table)
		}
	}
}

// A protected name that no migration creates is a name that has been renamed or
// removed, and a stale entry reads as protection that is not there.
func TestProtectedTables_everyEntryStillExists(t *testing.T) {
	tables := coreTables(t)

	for table := range protectedTables {
		// orama_schema_migrations is created by the namespace migration
		// runner, not by a migration file.
		if table == "orama_schema_migrations" {
			continue
		}
		if !tables[table] {
			t.Errorf("%q is protected but the migrations leave no such table; a stale entry reads as protection that is not there", table)
		}
	}
}

// The reachable list is the other half of the triage, and nothing was checking
// it against the schema. deployment_env_vars sat on it after migration 046
// dropped the table, which reads as a decision that a function may name it
// when there is nothing to name.
func TestReachableTables_allStillExist(t *testing.T) {
	tables := coreTables(t)

	for table := range reachableTables {
		if !tables[table] {
			t.Errorf("%q is listed as reachable but the migrations leave no such table; "+
				"a stale entry reads as a decision about something that is not there", table)
		}
	}
}

// coreTables returns the tables the core migrations leave behind: every table
// they create, less every table they later drop.
//
// Reading the CREATEs alone is not the same thing. Migration 046 dropped
// deployment_env_vars and 049 dropped phantom_auth_sessions, and a set built
// from CREATEs still contains both — so a dropped table has to be declared
// either protected or reachable, and both readings are false. A name that is
// not there is neither.
func coreTables(t *testing.T) map[string]bool {
	t.Helper()

	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	// ReadDir sorts by name, and migrations are numbered, so the statements
	// are visited in the order the runner applies them. A rename counts as
	// both: 009 builds dns_records_new, drops dns_records and renames the new
	// one into its place.
	ident := `["'` + "`" + `\[]?([a-z_][a-z0-9_]*)`
	stmt := regexp.MustCompile(`(?i)(create|drop)\s+table\s+(?:if\s+(?:not\s+)?exists\s+)?` + ident)
	rename := regexp.MustCompile(`(?i)alter\s+table\s+` + ident + `\s+rename\s+to\s+` + ident)

	tables := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		body, err := migrations.FS.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		text := string(body)
		for _, m := range stmt.FindAllStringSubmatch(text, -1) {
			name := strings.ToLower(m[2])
			if strings.EqualFold(m[1], "create") {
				tables[name] = true
			} else {
				delete(tables, name)
			}
		}
		for _, m := range rename.FindAllStringSubmatch(text, -1) {
			delete(tables, strings.ToLower(m[1]))
			tables[strings.ToLower(m[2])] = true
		}
	}
	if len(tables) < 30 {
		t.Fatalf("found only %d core tables; this test is not reading the migrations", len(tables))
	}
	return tables
}

// The denylist and the table placement have to agree in one direction: a table
// that exists only in the cluster registry is platform state by definition, and
// a function must not name it.
//
// It is a cross-check rather than a derivation because the two lists answer
// different questions. Placement says which database a table lives in; this list
// says which names guest SQL may not use, and it also covers tables that do live
// in a namespace database and still hold the platform's secrets —
// `function_secrets` is the clearest.
func TestProtectedTables_coverEveryClusterOnlyTable(t *testing.T) {
	for _, table := range rqlite.ClusterOnlyTables() {
		if _, protected := protectedTables[table]; !protected {
			t.Errorf("%q lives only in the cluster registry and guest SQL may still name it. "+
				"Stripping it from a namespace database makes the query fail anyway, but the "+
				"refusal should say what it is rather than 'no such table'.", table)
		}
	}
}
