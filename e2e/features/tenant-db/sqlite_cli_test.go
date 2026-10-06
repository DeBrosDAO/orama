//go:build e2e_fleet

package tenantdb

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// dbList is `orama db list --json`: the gateway's reply verbatim
// (core/cmd/orama/internal/db/commands.go listDatabases).
type dbList struct {
	Databases []struct {
		DatabaseName string `json:"database_name"`
	} `json:"databases"`
}

func listedDBs(t testing.TB, cli *oramacli.Runner) []string {
	t.Helper()
	var out dbList
	if err := oramacli.DecodeJSON(cli.MustOK(t, "db", "list", "--json"), &out); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range out.Databases {
		names = append(names, d.DatabaseName)
	}
	return names
}

// TestDBCLI_lifecycle drives `orama db` as an operator would: create, query
// (DDL, write, read), list, backup, backups, delete (docs/CLI_REFERENCE.md
// "orama db").
func TestDBCLI_lifecycle(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	cli := n.CLI
	cli.MustOK(t, "db", "create", "cli_db")
	t.Cleanup(func() { deleteViaCLI(t, cli, "cli_db") })
	if !strings.Contains(cli.MustOK(t, "db", "list").Stdout, "cli_db") {
		t.Fatal("orama db list does not show the new database")
	}
	cli.MustOK(t, "db", "query", "cli_db", "CREATE TABLE kv (k TEXT PRIMARY KEY, v TEXT)")
	if out := cli.MustOK(t, "db", "query", "cli_db", "INSERT INTO kv VALUES ('greeting', 'hello')").Stdout; !strings.Contains(out, "Rows affected: 1") {
		t.Fatalf("insert printed %q", out)
	}
	if out := cli.MustOK(t, "db", "query", "cli_db", "SELECT v FROM kv").Stdout; !strings.Contains(out, "hello") || !strings.Contains(out, "Rows returned: 1") {
		t.Fatalf("select printed %q", out)
	}
	backup := cli.MustOK(t, "db", "backup", "cli_db").Stdout
	cid := afterLabel(backup, "Backup CID:")
	if cid == "" {
		t.Fatalf("orama db backup printed no CID: %q", backup)
	}
	if out := cli.MustOK(t, "db", "backups", "cli_db").Stdout; !strings.Contains(out, cid[:min(len(cid), 20)]) {
		t.Fatalf("orama db backups does not list %s: %q", cid, out)
	}
	cli.MustOK(t, "db", "delete", "cli_db", "--yes")
	for _, name := range listedDBs(t, cli) {
		if name == "cli_db" {
			t.Fatal("the deleted database is still listed")
		}
	}
}

// TestDBCLI_refusals: a bad name, a missing database, a duplicate create, a
// refused statement and an unconfirmed delete all fail with a non-zero exit
// and change nothing.
func TestDBCLI_refusals(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{Via: ns.ViaOperator})
	cli := n.CLI
	cli.MustOK(t, "db", "create", "keep")
	t.Cleanup(func() { deleteViaCLI(t, cli, "keep") })
	for _, args := range [][]string{
		{"db", "create", "../escape"},
		{"db", "create", "keep"},
		{"db", "query", "missing", "SELECT 1"},
		{"db", "query", "keep", "SELECT 1; SELECT 2"},
		{"db", "query", "keep", "ATTACH DATABASE '/etc/passwd' AS x"},
		{"db", "backups", "missing"},
		{"db", "delete", "keep"}, // no --yes and nothing typed: nothing is deleted
		{"db", "create"},
		{"db", "query", "keep"},
	} {
		res, err := cli.Run(t.Context(), args...)
		if err != nil {
			t.Fatal(err)
		}
		if res.Exit == 0 && !strings.Contains(res.Stdout, "Nothing was deleted") {
			t.Errorf("orama %s succeeded: %s", strings.Join(args, " "), res.Stdout)
		}
	}
	if got := listedDBs(t, cli); len(got) != 1 || got[0] != "keep" {
		t.Fatalf("after the refusals the namespace holds %v, want [keep]", got)
	}
}

func TestDBCLI_groupListsSubcommands(t *testing.T) {
	t.Parallel()
	out := harness.CLI(t).MustOK(t, "db", "--help").Stdout
	for _, sub := range []string{"backup", "backups", "create", "delete", "list", "query"} {
		if !strings.Contains(out, sub) {
			t.Errorf("orama db --help does not list %s", sub)
		}
	}
}

func deleteViaCLI(t testing.TB, cli *oramacli.Runner, name string) {
	t.Helper()
	// Already deleted by the test is fine; anything else is reported.
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	res, err := cli.Run(ctx, "db", "delete", name, "--yes")
	if err != nil || (res.Exit != 0 && !strings.Contains(res.Stderr+res.Stdout, "not found")) {
		t.Errorf("cleanup: orama db delete %s exited %d: %v %s", name, res.Exit, err, res.Stderr)
	}
}

// afterLabel returns the first field after label on the line that has it.
func afterLabel(out, label string) string {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), label); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
