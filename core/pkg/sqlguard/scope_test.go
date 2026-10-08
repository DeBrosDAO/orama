package sqlguard

import (
	"errors"
	"testing"
)

var (
	scopeKnown   = map[string]bool{"deployments": true, "dns_records": true, "api_keys": true, "encryption_roots": true}
	scopeAllowed = map[string]bool{"deployments": true, "dns_records": true}
)

func TestCheckScope_allowsTheScopedTables(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM deployments WHERE id = ?",
		"insert into dns_records (fqdn) values (?)",
		"UPDATE deployments SET status = 'active' WHERE id = ?;",
		"DELETE FROM dns_records WHERE deployment_id = ?",
		"WITH d AS (SELECT id FROM deployments) SELECT id FROM d",
		"SELECT d.id FROM deployments d JOIN dns_records r ON r.deployment_id = d.id",
		"SELECT * FROM not_a_registry_table",
	} {
		if err := CheckScope(q, scopeAllowed, scopeKnown); err != nil {
			t.Errorf("%q was refused: %v", q, err)
		}
	}
}

func TestCheckScope_refusesATableOutsideTheScope(t *testing.T) {
	for _, q := range []string{
		"SELECT * FROM api_keys",
		"select * from API_KEYS",
		`SELECT * FROM "api_keys"`,
		"SELECT * FROM `api_keys`",
		"SELECT * FROM [api_keys]",
		"SELECT * FROM 'api_keys'",
		"SELECT * FROM deployments, api_keys",
		"SELECT id FROM deployments WHERE id IN (SELECT key_hash FROM api_keys)",
		"UPDATE encryption_roots SET ikm = ''",
		"SELECT /* hidden */ ikm FROM encryption_roots",
		"DELETE FROM deployments WHERE id IN (SELECT id FROM api_keys)",
	} {
		var refused *ErrNotAllowed
		if err := CheckScope(q, scopeAllowed, scopeKnown); !errors.As(err, &refused) {
			t.Errorf("%q was not refused: %v", q, err)
		}
	}
}

func TestCheckScope_refusesWhatIsNotPlainDataManipulation(t *testing.T) {
	for _, q := range []string{
		"DROP TABLE deployments",
		"ALTER TABLE deployments ADD COLUMN x TEXT",
		"CREATE TABLE evil (id TEXT)",
		"CREATE TRIGGER t AFTER INSERT ON deployments BEGIN DELETE FROM deployments; END",
		"PRAGMA table_info(deployments)",
		"ATTACH DATABASE '/tmp/x' AS x",
		"VACUUM INTO '/tmp/x'",
		"SELECT * FROM sqlite_master",
		"SELECT * FROM sqlite_schema",
		"SELECT * FROM pragma_table_info('deployments')",
		"SELECT * FROM dbstat",
		"SELECT 1; SELECT * FROM deployments",
		"",
		"  ;  ",
	} {
		var refused *ErrNotAllowed
		if err := CheckScope(q, scopeAllowed, scopeKnown); !errors.As(err, &refused) {
			t.Errorf("%q was not refused: %v", q, err)
		}
	}
}

func TestCheckScope_aTrailingSemicolonIsOneStatement(t *testing.T) {
	if err := CheckScope("SELECT * FROM deployments;;", scopeAllowed, scopeKnown); err != nil {
		t.Errorf("a trailing semicolon was refused: %v", err)
	}
}

func TestCheckScope_aNilScopeAllowsNoKnownTable(t *testing.T) {
	if err := CheckScope("SELECT * FROM deployments", nil, scopeKnown); err == nil {
		t.Error("a handle with no tables in scope reached a table")
	}
}
