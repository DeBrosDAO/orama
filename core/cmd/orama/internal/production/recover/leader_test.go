package recover

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// Which node is kept decides which copy of the cluster's data survives — every
// other node's raft log and database are deleted. The command required the
// operator to work that out by hand, across six nodes, while quorum was
// already lost, with no guidance beyond a curl in a runbook.

func TestParseAppliedIndex(t *testing.T) {
	body := []byte(`{"store":{"raft":{"state":"Follower","applied_index":"4711"}}}`)

	applied, state, err := parseAppliedIndex(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if applied != 4711 {
		t.Errorf("applied = %d, want 4711", applied)
	}
	if state != "Follower" {
		t.Errorf("state = %q, want Follower", state)
	}
}

// rqlite reports applied_index as a string. Reading it as a number would give
// zero for every node, and the recovery would keep whichever host sorted first.
func TestParseAppliedIndex_readsTheStringForm(t *testing.T) {
	applied, _, err := parseAppliedIndex([]byte(`{"store":{"raft":{"state":"Leader","applied_index":"99999999"}}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if applied != 99999999 {
		t.Errorf("applied = %d, want 99999999", applied)
	}
}

// A node with no raft state has not answered usefully, and treating it as
// applied-index-zero would silently rank it last rather than say it is unknown.
func TestParseAppliedIndex_rejectsABodyWithNoState(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"store":{}}`,
		`{"store":{"raft":{}}}`,
		`not json`,
	} {
		if _, _, err := parseAppliedIndex([]byte(body)); err == nil {
			t.Errorf("parseAppliedIndex(%q) must fail", body)
		}
	}
}

// A node that is up but has applied nothing is a real answer, not an error.
func TestParseAppliedIndex_zeroIsValid(t *testing.T) {
	applied, state, err := parseAppliedIndex([]byte(`{"store":{"raft":{"state":"Follower"}}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if applied != 0 || state != "Follower" {
		t.Errorf("got applied %d state %q", applied, state)
	}
}

func node(host string) inspector.Node { return inspector.Node{Host: host} }

func TestFormatIndexes_marksTheChosenNodeAndSaysWhatHappens(t *testing.T) {
	indexes := []nodeIndex{
		{Node: node("10.0.0.1"), Applied: 100, State: "Follower"},
		{Node: node("10.0.0.2"), Applied: 500, State: "Follower"},
		{Node: node("10.0.0.3"), Err: errUnreachable},
	}

	out := FormatIndexes(indexes, node("10.0.0.2"))

	if !strings.Contains(out, "→ 10.0.0.2") {
		t.Errorf("the kept node must be marked:\n%s", out)
	}
	if !strings.Contains(out, "500") || !strings.Contains(out, "100") {
		t.Errorf("every node's index must be shown:\n%s", out)
	}
	if !strings.Contains(out, "unreachable") {
		t.Errorf("a node that could not be read must say so:\n%s", out)
	}
	if !strings.Contains(out, "deleted") {
		t.Errorf("the operator must be told the other nodes are deleted:\n%s", out)
	}
}

// errUnreachable stands in for a node whose rqlite did not answer.
var errUnreachable = &unreachableErr{}

type unreachableErr struct{}

func (*unreachableErr) Error() string { return "rqlite did not answer" }

// pickFrom runs the selection over already-read indexes, which is the part
// worth testing: readAppliedIndexes needs six SSH connections.
func pickFrom(indexes []nodeIndex) (inspector.Node, bool) {
	var best nodeIndex
	found := false
	for _, ni := range indexes {
		if ni.Err != nil {
			continue
		}
		if !found || ni.Applied > best.Applied ||
			(ni.Applied == best.Applied && ni.Node.Host < best.Node.Host) {
			best, found = ni, true
		}
	}
	return best.Node, found
}

func TestPickFrom_keepsTheFurthestAhead(t *testing.T) {
	got, ok := pickFrom([]nodeIndex{
		{Node: node("10.0.0.1"), Applied: 100},
		{Node: node("10.0.0.2"), Applied: 900},
		{Node: node("10.0.0.3"), Applied: 500},
	})
	if !ok || got.Host != "10.0.0.2" {
		t.Errorf("got %q, want the node with applied 900", got.Host)
	}
}

// A node that could not be read has an unknown index, and unknown is not zero:
// choosing it would delete the data of nodes that answered.
func TestPickFrom_ignoresUnreachableNodes(t *testing.T) {
	got, ok := pickFrom([]nodeIndex{
		{Node: node("10.0.0.1"), Err: errUnreachable},
		{Node: node("10.0.0.2"), Applied: 7},
	})
	if !ok || got.Host != "10.0.0.2" {
		t.Errorf("got %q, want the node that answered", got.Host)
	}
}

// The same cluster has to produce the same answer twice, or two operators
// running the command a minute apart keep different copies of the data.
func TestPickFrom_tiesAreBrokenStably(t *testing.T) {
	for _, order := range [][]nodeIndex{
		{{Node: node("10.0.0.3"), Applied: 5}, {Node: node("10.0.0.1"), Applied: 5}},
		{{Node: node("10.0.0.1"), Applied: 5}, {Node: node("10.0.0.3"), Applied: 5}},
	} {
		got, _ := pickFrom(order)
		if got.Host != "10.0.0.1" {
			t.Errorf("got %q, want 10.0.0.1 regardless of input order", got.Host)
		}
	}
}

func TestPickFrom_nothingReachable(t *testing.T) {
	if _, ok := pickFrom([]nodeIndex{{Node: node("10.0.0.1"), Err: errUnreachable}}); ok {
		t.Error("with no node readable there is nothing to choose")
	}
}

// An explicit --leader is the operator overriding the automatic answer, and a
// name that is not in the environment is a mistake worth catching before
// anything is stopped.
func TestChooseLeader_explicitMustBeAKnownNode(t *testing.T) {
	nodes := []inspector.Node{node("10.0.0.1"), node("10.0.0.2")}

	got, err := chooseLeader(nodes, "10.0.0.2")
	if err != nil {
		t.Fatalf("chooseLeader: %v", err)
	}
	if got.Host != "10.0.0.2" {
		t.Errorf("got %q, want the named node", got.Host)
	}

	if _, err := chooseLeader(nodes, "10.0.0.9"); err == nil {
		t.Error("a --leader that is not in the environment must be refused")
	}
}
