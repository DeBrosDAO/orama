//go:build e2e_fleet

package infra

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// QueryResult is one statement's result from rqlite's /db/query.
type QueryResult struct {
	Columns []string `json:"columns"`
	Values  [][]any  `json:"values"`
	Error   string   `json:"error"`
}

// IndexQuery runs one parameterized read on n's index rqlite at strong
// consistency, the way operator commands do: curl on the node, the
// credentials read from node.yaml and handed to curl on stdin, never on a
// command line and never off the node (core/pkg/rqlite/shell.go).
func IndexQuery(t testing.TB, f *fleet.Fleet, n fleet.Node, sql string, args ...any) QueryResult {
	t.Helper()
	res, err := IndexQueryAt(t, f, n, "strong", sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// IndexQueryAt runs sql at the given read consistency level (none, weak,
// strong, linearizable) and returns the rqlite error as an error.
func IndexQueryAt(t testing.TB, f *fleet.Fleet, n fleet.Node, level, sql string, args ...any) (QueryResult, error) {
	t.Helper()
	cmd, err := indexQueryCommand(level, sql, args...)
	if err != nil {
		return QueryResult{}, err
	}
	return parseIndexQuery(f, n, f.Exec(t, n, cmd))
}

// IndexQueryInCleanup is IndexQueryAt (strong) for a t.Cleanup, where
// t.Context() is already cancelled: it runs on a context of its own and
// reports a command that could not run as an error, not a Fatal.
func IndexQueryInCleanup(t testing.TB, f *fleet.Fleet, n fleet.Node, sql string, args ...any) (QueryResult, error) {
	t.Helper()
	cmd, err := indexQueryCommand("strong", sql, args...)
	if err != nil {
		return QueryResult{}, err
	}
	ctx, cancel := fleet.CleanupContext(t)
	defer cancel()
	out, err := f.SSHFor(t, n).Run(ctx, cmd)
	if err != nil {
		return QueryResult{}, fmt.Errorf("%s: failed to run the index rqlite query: %w", n.Name, err)
	}
	return parseIndexQuery(f, n, out)
}

func indexQueryCommand(level, sql string, args ...any) (string, error) {
	stmt, err := json.Marshal([][]any{append([]any{sql}, args...)})
	if err != nil {
		return "", fmt.Errorf("encode the statement: %w", err)
	}
	opts := "-sS --max-time 20 -X POST -H 'Content-Type: application/json' --data-binary " + fleet.ShellQuote(string(stmt))
	return rqlite.NodeShellCurl("", opts, "/db/query?level="+level), nil
}

func parseIndexQuery(f *fleet.Fleet, n fleet.Node, out fleet.Output) (QueryResult, error) {
	if out.Exit != 0 {
		return QueryResult{}, fmt.Errorf("%s: index rqlite query failed (exit %d): %s", n.Name, out.Exit, f.Redact(out.Stderr))
	}
	var body struct {
		Results []QueryResult `json:"results"`
		Error   string        `json:"error"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &body); err != nil {
		return QueryResult{}, fmt.Errorf("%s: rqlite answered %q: %w", n.Name, f.Redact(out.Stdout), err)
	}
	if body.Error != "" || len(body.Results) != 1 || body.Results[0].Error != "" {
		return QueryResult{}, fmt.Errorf("%s: rqlite refused the query: %s", n.Name, f.Redact(out.Stdout))
	}
	return body.Results[0], nil
}

// IndexStatusCurl is the node-side command reading the index rqlite's
// /status with the node's credentials.
func IndexStatusCurl() string {
	return rqlite.NodeShellCurl("", "-sS --max-time 20", "/status")
}

// IndexCurl is the node-side authenticated curl of path with opts.
func IndexCurl(opts, path string) string {
	return rqlite.NodeShellCurl("", opts, path)
}
