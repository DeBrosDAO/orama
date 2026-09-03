package decommission

import (
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

func nodes(hosts ...string) []inspector.Node {
	out := make([]inspector.Node, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, inspector.Node{Host: h})
	}
	return out
}

// A node cannot retire itself from the cluster it is being removed from, so the
// removal has to be driven from somewhere else.
func TestPickSurvivor(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []inspector.Node
		target  string
		want    string
		wantErr bool
	}{
		{"picks another node", nodes("1.1.1.1", "2.2.2.2", "3.3.3.3"), "2.2.2.2", "1.1.1.1", false},
		{"skips the target when it is first", nodes("1.1.1.1", "2.2.2.2"), "1.1.1.1", "2.2.2.2", false},
		{"a single-node environment has no survivor", nodes("1.1.1.1"), "1.1.1.1", "", true},
		{"an empty environment has no survivor", nil, "1.1.1.1", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickSurvivor(tc.nodes, tc.target)
			if (err != nil) != tc.wantErr {
				t.Fatalf("pickSurvivor err = %v, wantErr %v", err, tc.wantErr)
			}
			if got.Host != tc.want {
				t.Fatalf("survivor = %q, want %q", got.Host, tc.want)
			}
		})
	}
}

func TestParseRaftMembers(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []rqlite.RaftMember
	}{
		{
			name: "ver=2 wrapped",
			body: `{"nodes":[{"id":"10.0.0.1:10101","voter":true,"reachable":true},
			                 {"id":"10.0.0.9:10101","voter":true,"reachable":false}]}`,
			want: []rqlite.RaftMember{
				{ID: "10.0.0.1:10101", Voter: true, Reachable: true},
				{ID: "10.0.0.9:10101", Voter: true, Reachable: false},
			},
		},
		{
			name: "plain array",
			body: `[{"id":"10.0.0.1:10101","voter":true,"reachable":true}]`,
			want: []rqlite.RaftMember{{ID: "10.0.0.1:10101", Voter: true, Reachable: true}},
		},
		{
			name: "empty wrapped",
			body: `{"nodes":[]}`,
			want: []rqlite.RaftMember{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRaftMembers([]byte(tc.body))
			if err != nil {
				t.Fatalf("parseRaftMembers: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("members = %+v, want %+v", got, tc.want)
			}
		})
	}

	if _, err := parseRaftMembers([]byte("not json")); err == nil {
		t.Error("unparseable /nodes output must be reported")
	}
}

// rqlite answers HTTP 200 with the failure inside the body, so a curl that
// "succeeded" says nothing about whether the statement did.
func TestCheckExecuteResponse(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"success", `{"results":[{"rows_affected":1}]}`, ""},
		{"top-level error", `{"error":"leader not found"}`, "leader not found"},
		{"per-statement error", `{"results":[{"error":"no such table: raft_evicted_nodes"}]}`, "no such table"},
		{"unparseable", `<html>502</html>`, "parse rqlite response"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkExecuteResponse([]byte(tc.body))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestFirstRow(t *testing.T) {
	body := `{"results":[{"columns":["id","internal_ip"],"types":["text","text"],
	          "values":[["12D3KooWNine","10.0.0.9"]]}]}`
	row, err := firstRow([]byte(body))
	if err != nil {
		t.Fatalf("firstRow: %v", err)
	}
	if asString(row[0]) != "12D3KooWNine" || asString(row[1]) != "10.0.0.9" {
		t.Fatalf("row = %v", row)
	}

	if _, err := firstRow([]byte(`{"results":[{"columns":["id"]}]}`)); err == nil {
		t.Error("a result set with no rows must be reported")
	}
	if _, err := firstRow([]byte(`{"error":"boom"}`)); err == nil {
		t.Error("an rqlite error must be reported")
	}
}

func TestAsString(t *testing.T) {
	if got := asString(nil); got != "" {
		t.Errorf("asString(nil) = %q", got)
	}
	if got := asString("x"); got != "x" {
		t.Errorf("asString(string) = %q", got)
	}
	// JSON numbers decode to float64.
	if got := asString(float64(51820)); got != "51820" {
		t.Errorf("asString(number) = %q", got)
	}
}

// These statements go over SSH into a curl body, so they cannot be
// parameterised. Escaping is not conditional on trusting the input.
func TestSQLLiteralAndShellQuote(t *testing.T) {
	if got := sqlLiteral("it's"); got != "it''s" {
		t.Errorf("sqlLiteral = %q", got)
	}
	if got := shellQuote(`a'b`); got != `'a'"'"'b'` {
		t.Errorf("shellQuote = %q", got)
	}
	// A quote in the value must not be able to close the shell's quoting.
	quoted := shellQuote(sqlLiteral(`'; DROP TABLE dns_nodes; --`))
	if strings.HasSuffix(quoted, `'`) && strings.Count(quoted, `'`) < 4 {
		t.Errorf("quoting collapsed: %q", quoted)
	}
}

// The wipe script's two fixes over the one it replaces.
func TestWipeScript(t *testing.T) {
	script := wipeScript(false)

	if !strings.Contains(script, `list-units --all --plain --no-legend "orama-namespace-*"`) {
		t.Error("the script must stop every namespace unit; those are template instances that match no legacy host unit name")
	}
	nsAt := strings.Index(script, "orama-namespace-*")
	rmAt := strings.Index(script, "rm -rf /opt/orama")
	if nsAt < 0 || rmAt < 0 || nsAt > rmAt {
		t.Error("namespace units must be stopped BEFORE the data directory is removed")
	}

	if strings.Contains(script, `pkill -9 -f "ipfs"`) {
		t.Error("an unanchored pkill pattern matches any command line mentioning ipfs")
	}
	if !strings.Contains(script, "/usr/local/bin/ipfs") {
		t.Error("the ipfs pkill pattern must be anchored to a full path")
	}

	if strings.Contains(script, "NUCLEAR=1") {
		t.Error("nuclear must be off unless asked for")
	}
	if !strings.Contains(wipeScript(true), "NUCLEAR=1") {
		t.Error("nuclear must be on when asked for")
	}
}
