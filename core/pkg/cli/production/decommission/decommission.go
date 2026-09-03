package decommission

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// Flags holds decommission command flags.
type Flags struct {
	Env     string
	Node    string
	Offline bool
	Nuclear bool
	Force   bool
}

// Handle is the entry point for `orama node decommission`.
func Handle(args []string) {
	flags, err := parseFlags(args)
	if err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := execute(flags); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags(args []string) (*Flags, error) {
	fs := flag.NewFlagSet("decommission", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flags := &Flags{}
	fs.StringVar(&flags.Env, "env", "", "Target environment (devnet, testnet) [required]")
	fs.StringVar(&flags.Node, "node", "", "Public IP of the node to remove [required]")
	fs.BoolVar(&flags.Offline, "offline", false, "The node is already gone: retire it cluster-side only, do not try to wipe it")
	fs.BoolVar(&flags.Nuclear, "nuclear", false, "When wiping, also remove shared binaries")
	fs.BoolVar(&flags.Force, "force", false, "Skip confirmation (DESTRUCTIVE)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if flags.Env == "" {
		return nil, fmt.Errorf("--env is required\nUsage: orama node decommission --env <devnet|testnet> --node <ip> [--offline] [--force]")
	}
	if flags.Node == "" {
		return nil, fmt.Errorf("--node is required: decommission removes ONE node")
	}
	return flags, nil
}

// pickSurvivor chooses the node to drive the cluster-side removal from.
//
// It must not be the target — a node cannot retire itself from a cluster it is
// being removed from — and the first survivor in the environment list is as
// good as any, because every step below is issued against the raft leader
// through that node's local rqlite, which forwards.
func pickSurvivor(nodes []inspector.Node, targetHost string) (inspector.Node, error) {
	for _, n := range nodes {
		if n.Host != targetHost {
			return n, nil
		}
	}
	return inspector.Node{}, fmt.Errorf(
		"no survivor to run the removal from: %s is the only node in this environment", targetHost)
}

func execute(flags *Flags) error {
	nodes, err := remotessh.LoadEnvNodes(flags.Env)
	if err != nil {
		return err
	}
	cleanup, err := remotessh.PrepareNodeKeys(nodes)
	if err != nil {
		return err
	}
	defer cleanup()

	targets := remotessh.FilterByIP(nodes, flags.Node)
	if len(targets) == 0 {
		return fmt.Errorf("node %s not found in the %s environment", flags.Node, flags.Env)
	}
	target := targets[0]

	survivor, err := pickSurvivor(nodes, target.Host)
	if err != nil {
		return err
	}

	fmt.Printf("Decommission %s from %s\n", target.Host, flags.Env)
	fmt.Printf("  Driving the cluster-side removal from %s\n", survivor.Host)
	if flags.Offline {
		fmt.Printf("  --offline: the node will NOT be wiped\n")
	}
	fmt.Println()

	if !flags.Force {
		fmt.Printf("This removes %s from raft, the mesh and the node registry", target.Host)
		if !flags.Offline {
			fmt.Printf(", then ERASES it")
		}
		fmt.Printf(".\nType 'yes' to confirm: ")
		input, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(input) != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
		fmt.Println()
	}

	record, err := resolveNodeRecord(survivor, target.Host)
	if err != nil {
		return err
	}
	fmt.Printf("  Node record: peer %s, overlay %s\n", record.PeerID, record.InternalIP)

	raftAddr := fmt.Sprintf("%s:%d", record.InternalIP, constants.RQLiteRaftPort)
	if err := removeRaftMember(survivor, raftAddr); err != nil {
		return err
	}

	if err := writeTombstone(survivor, raftAddr, record.PeerID, survivor.Host); err != nil {
		return err
	}
	fmt.Printf("  ✓ tombstoned, so nothing re-adds it automatically\n")

	if err := deleteMembershipRows(survivor, record); err != nil {
		return err
	}
	fmt.Printf("  ✓ removed from wireguard_peers and dns_nodes\n")

	if flags.Offline {
		fmt.Printf("\n✓ %s retired cluster-side. It was not wiped (--offline).\n", target.Host)
		return nil
	}

	fmt.Printf("\n  Wiping %s...\n", target.Host)
	if err := wipeNode(target, flags.Nuclear); err != nil {
		return fmt.Errorf("the node was retired cluster-side but the wipe failed: %w\n"+
			"  Re-run `orama node wipe --env %s --node %s` once it is reachable", err, flags.Env, target.Host)
	}

	fmt.Printf("\n✓ %s decommissioned and wiped\n", target.Host)
	fmt.Printf("  rm -rf is unlink, not cryptographic erase. Provider disks remain readable.\n")
	return nil
}

// nodeRecord is what dns_nodes knows about the target.
type nodeRecord struct {
	PeerID     string
	InternalIP string
}

// resolveNodeRecord finds the target's peer id and overlay address.
//
// Both are needed and neither is derivable from the public IP the operator
// typed: the overlay address identifies the raft member and the WireGuard peer,
// and the peer id is what the tombstone and every health record are keyed on.
func resolveNodeRecord(survivor inspector.Node, publicIP string) (nodeRecord, error) {
	out, err := querySQL(survivor,
		fmt.Sprintf("SELECT id, COALESCE(internal_ip, '') FROM dns_nodes WHERE ip_address = '%s'", sqlLiteral(publicIP)))
	if err != nil {
		return nodeRecord{}, err
	}

	values, err := firstRow(out)
	if err != nil {
		return nodeRecord{}, fmt.Errorf("no dns_nodes row for %s: %w\n"+
			"  If the node was never registered there is nothing to retire; wipe it directly with `orama node wipe`", publicIP, err)
	}
	if len(values) < 2 {
		return nodeRecord{}, fmt.Errorf("unexpected dns_nodes row shape for %s", publicIP)
	}

	rec := nodeRecord{PeerID: asString(values[0]), InternalIP: asString(values[1])}
	if rec.InternalIP == "" {
		return rec, fmt.Errorf("node %s has no overlay address recorded; cannot identify its raft member", publicIP)
	}
	return rec, nil
}

// removeRaftMember takes the node out of the raft configuration, refusing if
// that would cost the cluster its quorum.
func removeRaftMember(survivor inspector.Node, raftAddr string) error {
	members, err := raftMembers(survivor)
	if err != nil {
		return err
	}

	present := false
	for _, m := range members {
		if m.ID == raftAddr {
			present = true
		}
	}
	if !present {
		fmt.Printf("  - not in the raft configuration, nothing to remove\n")
		return nil
	}

	// The same rule the automatic eviction applies. An operator asking for
	// something unsafe gets the arithmetic, not a surprise outage.
	if refusal := rqlite.SafeToRemoveVoter(members, raftAddr); refusal != "" {
		return fmt.Errorf("refusing to remove %s from raft: %s", raftAddr, refusal)
	}

	cmd := fmt.Sprintf(
		`curl -sS --max-time 15 -XDELETE 'http://localhost:%d/remove' -H 'Content-Type: application/json' -d '{"id":"%s"}'`,
		constants.RQLiteHTTPPort, raftAddr)
	res := inspector.RunSSH(context.Background(), survivor, cmd)
	if !res.OK() {
		return fmt.Errorf("remove %s from raft: %v (stderr: %s)", raftAddr, res.Err, res.Stderr)
	}
	fmt.Printf("  ✓ removed from the raft configuration\n")
	return nil
}

// raftMembers reads the raft configuration from the survivor.
func raftMembers(survivor inspector.Node) ([]rqlite.RaftMember, error) {
	cmd := fmt.Sprintf("curl -sS --max-time 10 'http://localhost:%d/nodes?nonvoters&ver=2&timeout=5s'",
		constants.RQLiteHTTPPort)
	res := inspector.RunSSH(context.Background(), survivor, cmd)
	if !res.OK() {
		return nil, fmt.Errorf("read /nodes on %s: %v (stderr: %s)", survivor.Host, res.Err, res.Stderr)
	}
	return parseRaftMembers([]byte(res.Stdout))
}

// raftEntry is one member as /nodes reports it.
type raftEntry struct {
	ID        string `json:"id"`
	Voter     bool   `json:"voter"`
	Reachable bool   `json:"reachable"`
}

// parseRaftMembers reads an rqlite /nodes response in either the ver=2 wrapped
// shape or the plain array.
func parseRaftMembers(body []byte) ([]rqlite.RaftMember, error) {
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
		out = append(out, rqlite.RaftMember{ID: e.ID, Voter: e.Voter, Reachable: e.Reachable})
	}
	return out
}

// writeTombstone records that this removal was deliberate.
//
// Without it the membership reconciler and orphan recovery treat the node as
// merely absent and put it back — orphan recovery within five minutes, which is
// how operator removals used to be undone.
func writeTombstone(survivor inspector.Node, raftAddr, peerID, evictedBy string) error {
	stmt := fmt.Sprintf(
		`INSERT INTO raft_evicted_nodes (node_id, raft_addr, peer_id, reason, evicted_by) `+
			`VALUES ('%s','%s','%s','operator','%s') `+
			`ON CONFLICT(node_id) DO UPDATE SET peer_id=excluded.peer_id, reason='operator', `+
			`evicted_by=excluded.evicted_by, evicted_at=CURRENT_TIMESTAMP`,
		sqlLiteral(raftAddr), sqlLiteral(raftAddr), sqlLiteral(peerID), sqlLiteral(evictedBy))
	return execSQL(survivor, stmt)
}

// deleteMembershipRows removes the node from the stores the reconciler would
// otherwise take hours to catch up on.
//
// The reconciler would get there on its own — that is the point of it — but an
// operator who asked for a removal should not have to wait out a liveness grace
// to see it happen.
func deleteMembershipRows(survivor inspector.Node, rec nodeRecord) error {
	stmts := []string{
		fmt.Sprintf(`DELETE FROM wireguard_peers WHERE wg_ip = '%s'`, sqlLiteral(rec.InternalIP)),
		fmt.Sprintf(`DELETE FROM dns_nodes WHERE id = '%s'`, sqlLiteral(rec.PeerID)),
	}
	for _, stmt := range stmts {
		if err := execSQL(survivor, stmt); err != nil {
			return err
		}
	}
	return nil
}

// sqlLiteral escapes a value for embedding in a single-quoted SQL literal.
//
// These statements go over SSH into a curl body, so they cannot be
// parameterised the way the Go paths are. Every value that reaches here is
// either an operator-supplied IP or a value read back out of the database, but
// escaping is not conditional on trusting the input.
func sqlLiteral(v string) string {
	return strings.ReplaceAll(v, "'", "''")
}

// execSQL runs one write statement against the survivor's rqlite, which
// forwards it to the leader.
func execSQL(survivor inspector.Node, stmt string) error {
	body, err := json.Marshal([]string{stmt})
	if err != nil {
		return fmt.Errorf("encode statement: %w", err)
	}
	cmd := fmt.Sprintf(`curl -sS --max-time 15 -XPOST 'http://localhost:%d/db/execute' `+
		`-H 'Content-Type: application/json' -d %s`, constants.RQLiteHTTPPort, shellQuote(string(body)))

	res := inspector.RunSSH(context.Background(), survivor, cmd)
	if !res.OK() {
		return fmt.Errorf("execute on %s: %v (stderr: %s)", survivor.Host, res.Err, res.Stderr)
	}
	return checkExecuteResponse([]byte(res.Stdout))
}

// querySQL runs one read statement and returns the decoded response.
func querySQL(survivor inspector.Node, stmt string) ([]byte, error) {
	cmd := fmt.Sprintf(`curl -sS --max-time 15 -G 'http://localhost:%d/db/query?level=strong' `+
		`--data-urlencode %s`, constants.RQLiteHTTPPort, shellQuote("q="+stmt))

	res := inspector.RunSSH(context.Background(), survivor, cmd)
	if !res.OK() {
		return nil, fmt.Errorf("query on %s: %v (stderr: %s)", survivor.Host, res.Err, res.Stderr)
	}
	return []byte(res.Stdout), nil
}

// shellQuote wraps s in single quotes for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
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
