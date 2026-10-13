package inspector

import (
	"database/sql"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// shellArgs returns what a POSIX shell makes of opts: the arguments curl gets.
func shellArgs(t *testing.T, opts string) []string {
	t.Helper()
	out, err := exec.Command("sh", "-c", "printf '%s\\n' "+opts).Output()
	if err != nil {
		t.Fatalf("sh could not parse %q: %v", opts, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

// The SQL travels inside a single-quoted shell word; the shell must hand curl
// the statement exactly as written, quotes included (live, an unquoted
// strftime('%s','now') became strftime(%s,now), a syntax error that left the
// registry map empty so no namespace was ever excused as in transition).
func TestNamespaceRegistryCurlOpts_shellDeliversTheStatementIntact(t *testing.T) {
	args := shellArgs(t, namespaceRegistryCurlOpts())
	if len(args) < 2 || args[len(args)-2] != "-d" {
		t.Fatalf("curl options %q do not end in -d <body>", args)
	}
	var body [][]string
	if err := json.Unmarshal([]byte(args[len(args)-1]), &body); err != nil {
		t.Fatalf("the body the shell delivers is not JSON: %v\n%s", err, args[len(args)-1])
	}
	if len(body) != 1 || len(body[0]) != 1 || body[0][0] != namespaceRegistrySQL {
		t.Fatalf("the shell delivered %q, want the statement %q", body, namespaceRegistrySQL)
	}
}

// The statement is valid SQL over the registry's columns and answers
// name, status and an integer age for each status.
func TestNamespaceRegistrySQL_answersAgeInSeconds(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE namespace_clusters (namespace_name TEXT, status TEXT, provisioned_at TIMESTAMP, deprovisioning_at TIMESTAMP)`,
		`INSERT INTO namespace_clusters VALUES ('new', 'provisioning', datetime('now', '-30 seconds'), NULL)`,
		`INSERT INTO namespace_clusters VALUES ('going', 'deprovisioning', datetime('now', '-3 hours'), datetime('now', '-45 seconds'))`,
		`INSERT INTO namespace_clusters VALUES ('settled', 'ready', datetime('now', '-120 seconds'), NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	rows, err := db.Query(namespaceRegistrySQL)
	if err != nil {
		t.Fatalf("the registry statement is not valid SQL: %v", err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var name, status string
		var age int
		if err := rows.Scan(&name, &status, &age); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name+"/"+status] = age
	}
	want := map[string]int{"new/provisioning": 30, "going/deprovisioning": 45, "settled/ready": 120}
	for k, w := range want {
		if g, ok := got[k]; !ok || g < w || g > w+2 {
			t.Errorf("%s: age %d (present %v), want about %d", k, g, ok, w)
		}
	}
}
