package lifecycle

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/utils"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// indexRQLiteUnit is the systemd unit backing the index RQLite on this node.
const indexRQLiteUnit = "orama-namespace-rqlite@index"

// legacyRQLiteSupervisor ran the index rqlited as a child process on 0.122.x.
const legacyRQLiteSupervisor = "orama-node"

// rqliteTemplateUnit is the template this release's index rqlite runs from.
// 0.122.x never installed it, which is how its layout is recognised.
const rqliteTemplateUnit = "/etc/systemd/system/orama-namespace-rqlite@.service"

// indexRQLiteUnits are the units an index rqlited can run under here: its own
// unit, and on a 0.122.x node — no rqlite template installed — orama-node,
// which ran rqlited itself. The upgrade from 0.122.x runs the hand-over on such
// a node, so "rqlited is not running" there has to mean neither is active. On
// this release orama-node is always active and never runs rqlited, so it is
// not counted: a node whose rqlite@index is down is stoppable without --force.
// A template that cannot be inspected counts orama-node, which only refuses.
func indexRQLiteUnits() []string {
	_, err := statUnit(rqliteTemplateUnit)
	if err == nil {
		return []string{indexRQLiteUnit}
	}
	return []string{indexRQLiteUnit, legacyRQLiteSupervisor}
}

// statUnit is os.Stat; a variable so the layout check can be tested.
var statUnit = os.Stat

// quorumHTTPTimeout bounds each control-plane read. Short, because this runs
// interactively in front of a stop and an operator waiting on a hung request is
// an operator who reaches for --force.
const quorumHTTPTimeout = 5 * time.Second

// checkQuorumSafety reports why stopping this node would be unsafe, or "" when
// it is safe. Callers refuse the operation on a non-empty result unless the
// operator passed --force.
//
// It fails CLOSED. The previous version returned "" — safe — whenever the local
// RQLite could not be read, which is the one situation where the answer is
// genuinely unknown. Combined with a stale port that made every read fail, the
// guard silently approved stopping the leader or two of three voters. A guard
// whose whole purpose is preventing quorum loss must not treat "I could not
// look" as "go ahead".
//
// The one case that is genuinely safe without a reading: rqlited is not
// running. Then this node is already contributing nothing to quorum and
// stopping the rest of the stack cannot remove a voter the cluster still counts
// on. That is checked explicitly rather than inferred from a failed request.
func checkQuorumSafety() string {
	warning, _ := quorumVerdict()
	return warning
}

// quorumVerdict is checkQuorumSafety, also reporting whether an index rqlited
// may be running: false only when its status could not be read and no unit
// that runs one is active.
func quorumVerdict() (warning string, rqliteMayRun bool) {
	status, statusErr := localRQLiteStatus()
	in := quorumInputs{status: status, statusErr: statusErr, rqliteRunning: true}
	if statusErr != nil {
		in.rqliteRunning = indexRQLiteMayRun()
	}
	if statusErr == nil && status.Store.Raft.Voter {
		in.nodes, in.nodesErr = localRQLiteNodes()
	}
	return evaluateQuorumSafety(in), in.rqliteRunning
}

// indexRQLiteMayRun reports whether any unit that runs an index rqlited is
// active. A failed check counts as active, so an unreadable systemd never
// becomes a way to approve a stop.
func indexRQLiteMayRun() bool {
	for _, unit := range indexRQLiteUnits() {
		active, err := serviceActive(unit)
		if err != nil || active {
			return true
		}
	}
	return false
}

// quorumInputs is everything the decision depends on, gathered separately so
// the policy below can be exercised without a live cluster.
type quorumInputs struct {
	status    *rqlite.RQLiteStatus
	statusErr error
	nodes     []rqliteNode
	nodesErr  error
	// rqliteRunning is only consulted when statusErr != nil. A failed check is
	// itself treated as "running", so an unreadable systemd never becomes a
	// second way to accidentally approve a stop.
	rqliteRunning bool
}

// evaluateQuorumSafety returns why stopping this node is unsafe, or "" when it
// is safe.
func evaluateQuorumSafety(in quorumInputs) string {
	if in.statusErr != nil {
		if !in.rqliteRunning {
			// rqlited is down, so this node already contributes nothing to
			// quorum and stopping the rest of the stack removes no voter the
			// cluster still counts on.
			return ""
		}
		return fmt.Sprintf(
			"Cannot verify quorum safety: the index rqlite may be running (%s) but its status could not be read (%v). "+
				"Stopping a voter blind can leave the cluster without quorum. "+
				"Check `orama node status`, or re-run with --force if you know this node is not a voter.",
			strings.Join(indexRQLiteUnits(), " or "), in.statusErr)
	}

	raft := in.status.Store.Raft

	// rqlite reports the node's own voter flag; a non-voter never counts toward
	// quorum, so stopping it is always safe.
	if !raft.Voter {
		return ""
	}

	if in.nodesErr != nil {
		return fmt.Sprintf(
			"Cannot verify quorum safety: this node is a %s VOTER but the cluster member list could not be read (%v). "+
				"Re-run with --force only if you have confirmed the remaining voters can still form quorum.",
			raft.State, in.nodesErr)
	}

	reachableVoters, totalVoters := countVoters(in.nodes)
	if totalVoters == 0 {
		return fmt.Sprintf(
			"Cannot verify quorum safety: this node is a %s VOTER but the cluster member list reported no voters at all. "+
				"That is not a state a healthy cluster produces; investigate before stopping anything.",
			raft.State)
	}

	// This node answered /status and reports itself a voter, so it is one of the
	// reachable voters counted above.
	//
	// Quorum is a majority of the CONFIGURED voters, and stopping a node does
	// not remove it from the raft configuration - it just makes it unreachable.
	// The previous version computed the threshold over totalVoters-1, as though
	// the node had been removed, which under-counted what the cluster needs: on
	// two voters it concluded that stopping one left "1 of 1, need 1" and
	// allowed it, when raft still requires 2 of 2 and the survivor cannot elect
	// a leader. Membership only shrinks through an explicit remove, which is
	// `orama node remove`, not a stop.
	remainingVoters := reachableVoters - 1
	quorumNeeded := totalVoters/2 + 1

	if remainingVoters < quorumNeeded {
		role := raft.State
		if role == "Leader" {
			role = "the LEADER"
		}
		return fmt.Sprintf(
			"Stopping this node (%s, voter) would break RQLite quorum: %d of %d configured voters would remain reachable, need %d.",
			role, remainingVoters, totalVoters, quorumNeeded)
	}

	if raft.State == "Leader" {
		fmt.Printf("  Note: this node is the RQLite leader; leadership transfers on shutdown.\n")
	}
	fmt.Printf("  Quorum check: %d/%d voters reachable, %d would remain (need %d).\n",
		reachableVoters, totalVoters, remainingVoters, quorumNeeded)

	return ""
}

// countVoters returns how many cluster members are voters and how many of those
// are currently reachable.
func countVoters(nodes []rqliteNode) (reachable, total int) {
	for _, n := range nodes {
		if !n.Voter {
			continue
		}
		total++
		if n.Reachable {
			reachable++
		}
	}
	return reachable, total
}

// Indirection so the policy above can be tested against a fake server and a
// fake systemd, without either being reachable.
var (
	localRQLite   = rqlite.LocalNodeEndpoint
	serviceActive = utils.IsServiceActive
)

// localRQLiteStatus reads the index RQLite's own view of itself.
func localRQLiteStatus() (*rqlite.RQLiteStatus, error) {
	body, err := quorumGet("/status")
	if err != nil {
		return nil, err
	}
	var status rqlite.RQLiteStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("decode status: %w", err)
	}
	if status.Store.Raft.State == "" {
		return nil, fmt.Errorf("status carried no raft state")
	}
	return &status, nil
}

// rqliteNode is one member of the cluster as reported by /nodes.
type rqliteNode struct {
	Voter     bool `json:"voter"`
	Reachable bool `json:"reachable"`
}

// localRQLiteNodes reads cluster membership, including non-voters so the voter
// total is not inflated by them.
func localRQLiteNodes() ([]rqliteNode, error) {
	body, err := quorumGet("/nodes?nonvoters&timeout=3s")
	if err != nil {
		return nil, err
	}
	// rqlite returns node_id -> node_info.
	var byID map[string]rqliteNode
	if err := json.Unmarshal(body, &byID); err != nil {
		return nil, fmt.Errorf("decode nodes: %w", err)
	}
	nodes := make([]rqliteNode, 0, len(byID))
	for _, n := range byID {
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// quorumGet reads path from this node's index rqlite, where it binds and with
// its credentials.
func quorumGet(path string) ([]byte, error) {
	ep, err := localRQLite()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, ep.BaseURL()+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", path, err)
	}
	if ep.Username != "" {
		req.SetBasicAuth(ep.Username, ep.Password)
	}
	client := &http.Client{Timeout: quorumHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// containsService checks if a service name exists in the service list
func containsService(services []string, name string) bool {
	for _, s := range services {
		if s == name {
			return true
		}
	}
	return false
}
