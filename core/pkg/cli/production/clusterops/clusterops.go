// Package clusterops holds the cluster-side operations an operator command
// performs against a surviving node: reading the raft configuration, removing a
// member under the quorum-safety rule, writing eviction tombstones, and running
// SQL over SSH.
//
// It exists because two commands need all of it — decommissioning a node and
// migrating a node's raft id — and a second copy of the quorum check is the one
// mistake this codebase can least afford. The version that is wrong is the one
// nobody reads.
package clusterops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// PickSurvivor chooses the node to drive the cluster-side removal from.
//
// It must not be the target — a node cannot retire itself from a cluster it is
// being removed from — and the first survivor in the environment list is as
// good as any, because every step below is issued against the raft leader
// through that node's local rqlite, which forwards.
func PickSurvivor(nodes []inspector.Node, targetHost string) (inspector.Node, error) {
	for _, n := range nodes {
		if n.Host != targetHost {
			return n, nil
		}
	}
	return inspector.Node{}, fmt.Errorf(
		"no survivor to run the removal from: %s is the only node in this environment", targetHost)
}

// ResolveNodeRecord finds the target's peer id and overlay address.
//
// Both are needed and neither is derivable from the public IP the operator
// typed: the overlay address identifies the raft member and the WireGuard peer,
// and the peer id is what the tombstone and every health record are keyed on.
func ResolveNodeRecord(survivor inspector.Node, publicIP string) (NodeRecord, error) {
	out, err := QuerySQL(survivor,
		fmt.Sprintf("SELECT id, COALESCE(internal_ip, '') FROM dns_nodes WHERE ip_address = '%s'", SQLLiteral(publicIP)))
	if err != nil {
		return NodeRecord{}, err
	}

	values, err := firstRow(out)
	if err != nil {
		return NodeRecord{}, fmt.Errorf("no dns_nodes row for %s: %w\n"+
			"  If the node was never registered there is nothing to retire; wipe it directly with `orama node wipe`", publicIP, err)
	}
	if len(values) < 2 {
		return NodeRecord{}, fmt.Errorf("unexpected dns_nodes row shape for %s", publicIP)
	}

	rec := NodeRecord{PeerID: asString(values[0]), InternalIP: asString(values[1])}
	if rec.InternalIP == "" {
		return rec, fmt.Errorf("node %s has no overlay address recorded; cannot identify its raft member", publicIP)
	}
	return rec, nil
}

// RemoveRaftMember takes the node out of the raft configuration, refusing if
// that would cost the cluster its quorum.
//
// nodeID is the raft ID, which on a node predating stable identity is its raft
// address and afterwards is its libp2p peer id. It is emphatically not "the
// address": rqlite's /remove keys on the id.
func RemoveRaftMember(survivor inspector.Node, nodeID string) error {
	members, err := RaftMembers(survivor)
	if err != nil {
		return err
	}

	present := false
	for _, m := range members {
		if m.ID == nodeID {
			present = true
		}
	}
	if !present {
		fmt.Printf("  - not in the raft configuration, nothing to remove\n")
		return nil
	}

	// The planned-removal rule, not the eviction one. An operator retiring a
	// node removes a member that is still answering on purpose; the eviction
	// check refuses exactly that, and using it here meant every healthy node
	// was refused. What still applies is the quorum arithmetic.
	if refusal := rqlite.SafeToRemoveMember(members, nodeID); refusal != "" {
		return fmt.Errorf("refusing to remove %s from raft: %s", nodeID, refusal)
	}

	cmd := fmt.Sprintf(
		`curl -sS --max-time 15 -XDELETE 'http://localhost:%d/remove' -H 'Content-Type: application/json' -d '{"id":"%s"}'`,
		constants.RQLiteHTTPPort, nodeID)
	res := inspector.RunSSH(context.Background(), survivor, cmd)
	if !res.OK() {
		return fmt.Errorf("remove %s from raft: %v (stderr: %s)", nodeID, res.Err, res.Stderr)
	}
	fmt.Printf("  ✓ removed from the raft configuration\n")
	return nil
}

// IDForAddr returns the raft id of the member advertising raftAddr, or "" when
// no member does.
//
// This is the bridge an operator command needs: it knows a node's overlay
// address, while rqlite keys members by id — which after the stable-identity
// migration is a peer id and before it is the address. Removing by address
// silently matched nothing on a migrated cluster and reported success.
func IDForAddr(members []rqlite.RaftMember, raftAddr string) string {
	for _, m := range members {
		if m.Addr == raftAddr {
			return m.ID
		}
	}
	return ""
}

// RaftMembers reads the raft configuration from the survivor.
func RaftMembers(survivor inspector.Node) ([]rqlite.RaftMember, error) {
	cmd := fmt.Sprintf("curl -sS --max-time 10 'http://localhost:%d/nodes?nonvoters&ver=2&timeout=5s'",
		constants.RQLiteHTTPPort)
	res := inspector.RunSSH(context.Background(), survivor, cmd)
	if !res.OK() {
		return nil, fmt.Errorf("read /nodes on %s: %v (stderr: %s)", survivor.Host, res.Err, res.Stderr)
	}
	return ParseRaftMembers([]byte(res.Stdout))
}

// ParseRaftMembers reads an rqlite /nodes response in either the ver=2 wrapped
// shape or the plain array.
func ParseRaftMembers(body []byte) ([]rqlite.RaftMember, error) {
	var wrapped struct {
		Nodes []raftEntry `json:"nodes"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Nodes != nil {
		return toMembers(wrapped.Nodes), nil
	}

	var plain []raftEntry
	if err := json.Unmarshal(body, &plain); err != nil {
		return nil, fmt.Errorf("parse /nodes response: %w", err)
	}
	return toMembers(plain), nil
}

func toMembers(entries []raftEntry) []rqlite.RaftMember {
	out := make([]rqlite.RaftMember, 0, len(entries))
	for _, e := range entries {
		out = append(out, rqlite.RaftMember{ID: e.ID, Addr: e.Addr, Voter: e.Voter, Reachable: e.Reachable})
	}
	return out
}

// WriteTombstone records that this removal was deliberate.
//
// Without it the membership reconciler and orphan recovery treat the node as
// merely absent and put it back — orphan recovery within five minutes, which is
// how operator removals used to be undone.
// nodeID and raftAddr are separate on purpose: they are equal only on a node
// that predates stable raft ids, and writing an id into the address column is
// the same conflation this whole change exists to end.
func WriteTombstone(survivor inspector.Node, nodeID, raftAddr, peerID, evictedBy string) error {
	stmt := fmt.Sprintf(
		`INSERT INTO raft_evicted_nodes (node_id, raft_addr, peer_id, reason, evicted_by) `+
			`VALUES ('%s','%s','%s','operator','%s') `+
			`ON CONFLICT(node_id) DO UPDATE SET raft_addr=excluded.raft_addr, peer_id=excluded.peer_id, `+
			`reason='operator', evicted_by=excluded.evicted_by, evicted_at=CURRENT_TIMESTAMP`,
		SQLLiteral(nodeID), SQLLiteral(raftAddr), SQLLiteral(peerID), SQLLiteral(evictedBy))
	return ExecSQL(survivor, stmt)
}

// SQLLiteral escapes a value for embedding in a single-quoted SQL literal.
//
// These statements go over SSH into a curl body, so they cannot be
// parameterised the way the Go paths are. Every value that reaches here is
// either an operator-supplied IP or a value read back out of the database, but
// escaping is not conditional on trusting the input.
func SQLLiteral(v string) string {
	return strings.ReplaceAll(v, "'", "''")
}

// ExecSQL runs one write statement against the survivor's rqlite, which
// forwards it to the leader.
func ExecSQL(survivor inspector.Node, stmt string) error {
	body, err := json.Marshal([]string{stmt})
	if err != nil {
		return fmt.Errorf("encode statement: %w", err)
	}
	cmd := fmt.Sprintf(`curl -sS --max-time 15 -XPOST 'http://localhost:%d/db/execute' `+
		`-H 'Content-Type: application/json' -d %s`, constants.RQLiteHTTPPort, ShellQuote(string(body)))

	res := inspector.RunSSH(context.Background(), survivor, cmd)
	if !res.OK() {
		return fmt.Errorf("execute on %s: %v (stderr: %s)", survivor.Host, res.Err, res.Stderr)
	}
	return checkExecuteResponse([]byte(res.Stdout))
}

// QuerySQL runs one read statement and returns the decoded response.
func QuerySQL(survivor inspector.Node, stmt string) ([]byte, error) {
	cmd := fmt.Sprintf(`curl -sS --max-time 15 -G 'http://localhost:%d/db/query?level=strong' `+
		`--data-urlencode %s`, constants.RQLiteHTTPPort, ShellQuote("q="+stmt))

	res := inspector.RunSSH(context.Background(), survivor, cmd)
	if !res.OK() {
		return nil, fmt.Errorf("query on %s: %v (stderr: %s)", survivor.Host, res.Err, res.Stderr)
	}
	return []byte(res.Stdout), nil
}

// ShellQuote wraps s in single quotes for a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// checkExecuteResponse turns an rqlite error payload into a Go error.
//
// rqlite answers HTTP 200 with the failure inside the body, so a curl that
// "succeeded" says nothing about whether the statement did.
func checkExecuteResponse(body []byte) error {
	var resp rqliteResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("parse rqlite response %q: %w", strings.TrimSpace(string(body)), err)
	}
	if resp.Error != "" {
		return fmt.Errorf("rqlite: %s", resp.Error)
	}
	for _, r := range resp.Results {
		if r.Error != "" {
			return fmt.Errorf("rqlite: %s", r.Error)
		}
	}
	return nil
}

// firstRow returns the first row of a query response.
func firstRow(body []byte) ([]any, error) {
	var resp rqliteResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse rqlite response %q: %w", strings.TrimSpace(string(body)), err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("rqlite: %s", resp.Error)
	}
	for _, r := range resp.Results {
		if r.Error != "" {
			return nil, fmt.Errorf("rqlite: %s", r.Error)
		}
		if len(r.Values) > 0 {
			return r.Values[0], nil
		}
	}
	return nil, fmt.Errorf("no rows")
}

// asString renders a JSON-decoded column value as a string.
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// NodeRecord is what dns_nodes knows about the target.
type NodeRecord struct {
	PeerID     string
	InternalIP string
}

// raftEntry is one member as /nodes reports it.
type raftEntry struct {
	ID        string `json:"id"`
	Addr      string `json:"addr"`
	Voter     bool   `json:"voter"`
	Reachable bool   `json:"reachable"`
}

// rqliteResponse is the shape of both /db/execute and /db/query replies.
type rqliteResponse struct {
	Results []struct {
		Error  string          `json:"error"`
		Values [][]any         `json:"values"`
		Types  []string        `json:"types"`
		Raw    json.RawMessage `json:"-"`
	} `json:"results"`
	Error string `json:"error"`
}

// ClearTombstone removes a raft eviction tombstone that has served its purpose.
//
// A tombstone stops orphan recovery re-adding a node that was removed on
// purpose. Once the node is back in the configuration under a new id the entry
// is spent, and leaving it means the eviction path reads a dead row every tick.
func ClearTombstone(survivor inspector.Node, nodeID string) error {
	return ExecSQL(survivor, fmt.Sprintf(
		`DELETE FROM raft_evicted_nodes WHERE node_id = '%s'`, SQLLiteral(nodeID)))
}
