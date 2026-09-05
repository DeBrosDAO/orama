package clusterops

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func nodes(hosts ...string) []inspector.Node {
	out := make([]inspector.Node, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, inspector.Node{Host: h})
	}
	return out
}

func TestParseRaftMembers_keepsIDAndAddressSeparate(t *testing.T) {
	// The whole point of stable identity: a member's id and its address are
	// different things, and a parser that drops the address makes it impossible
	// to tell which machine a member is.
	body := []byte(`{"nodes":[
	  {"id":"12D3KooWAlpha","addr":"10.0.0.2:10101","voter":true,"reachable":true},
	  {"id":"10.0.0.3:10101","addr":"10.0.0.3:10101","voter":true,"reachable":true}
	]}`)

	members, err := ParseRaftMembers(body)
	if err != nil {
		t.Fatalf("ParseRaftMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}

	if members[0].ID != "12D3KooWAlpha" || members[0].Addr != "10.0.0.2:10101" {
		t.Fatalf("migrated member = %+v, want id and address to differ", members[0])
	}
	if members[1].ID != members[1].Addr {
		t.Fatalf("un-migrated member = %+v, want id equal to address", members[1])
	}
}

func TestParseRaftMembers_plainArrayShape(t *testing.T) {
	body := []byte(`[{"id":"12D3KooWAlpha","addr":"10.0.0.2:10101","voter":true,"reachable":false}]`)

	members, err := ParseRaftMembers(body)
	if err != nil {
		t.Fatalf("ParseRaftMembers: %v", err)
	}
	if len(members) != 1 || members[0].Addr != "10.0.0.2:10101" || members[0].Reachable {
		t.Fatalf("got %+v", members)
	}
}

func TestParseRaftMembers_rejectsGarbage(t *testing.T) {
	if _, err := ParseRaftMembers([]byte("not json")); err == nil {
		t.Fatal("expected an error rather than an empty member list — " +
			"an empty list reads as 'the cluster has no members' and every quorum check would pass")
	}
}

func TestParseRaftMembers_emptyCluster(t *testing.T) {
	members, err := ParseRaftMembers([]byte(`{"nodes":[]}`))
	if err != nil {
		t.Fatalf("ParseRaftMembers: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("got %d members, want 0", len(members))
	}
}

func TestSQLLiteral_escapesQuotes(t *testing.T) {
	tests := map[string]string{
		"plain":     "plain",
		"O'Brien":   "O''Brien",
		"'; DROP--": "''; DROP--",
		"":          "",
	}
	for in, want := range tests {
		if got := SQLLiteral(in); got != want {
			t.Fatalf("SQLLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"plain":    "'plain'",
		"it's":     `'it'"'"'s'`,
		"a b":      "'a b'",
		"$(uname)": "'$(uname)'",
	}
	for in, want := range tests {
		if got := ShellQuote(in); got != want {
			t.Fatalf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
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
			got, err := PickSurvivor(tc.nodes, tc.target)
			if (err != nil) != tc.wantErr {
				t.Fatalf("PickSurvivor err = %v, wantErr %v", err, tc.wantErr)
			}
			if got.Host != tc.want {
				t.Fatalf("survivor = %q, want %q", got.Host, tc.want)
			}
		})
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

func TestIDForAddr(t *testing.T) {
	// The bridge an operator command needs: it knows an overlay address, rqlite
	// keys members by id. Removing by address matched nothing on a migrated
	// cluster and reported success, leaving a retired machine a voter for ever.
	members, err := ParseRaftMembers([]byte(`{"nodes":[
	  {"id":"12D3KooWAlpha","addr":"10.0.0.2:10101","voter":true,"reachable":true},
	  {"id":"10.0.0.3:10101","addr":"10.0.0.3:10101","voter":true,"reachable":true}
	]}`))
	if err != nil {
		t.Fatalf("ParseRaftMembers: %v", err)
	}

	if got := IDForAddr(members, "10.0.0.2:10101"); got != "12D3KooWAlpha" {
		t.Fatalf("migrated member: got %q, want its peer id", got)
	}
	if got := IDForAddr(members, "10.0.0.3:10101"); got != "10.0.0.3:10101" {
		t.Fatalf("un-migrated member: got %q", got)
	}
	if got := IDForAddr(members, "10.0.0.9:10101"); got != "" {
		t.Fatalf("unknown address: got %q, want empty", got)
	}
}
