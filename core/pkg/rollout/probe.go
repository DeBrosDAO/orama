package rollout

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/cli/remotessh"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/nodehealth"
)

// SSHRunner runs a command on a node and returns its stdout. A function type so
// the plan and the gate are testable without an SSH server.
type SSHRunner func(node inspector.Node, command string) (string, error)

// DefaultRunner runs over the operator's SSH connection.
func DefaultRunner(node inspector.Node, command string) (string, error) {
	return remotessh.RunSSHOutput(node, command)
}

// Gate budgets.
// GatePollInterval is how often the gate re-reads the node. A var only so
// tests can drive it without sleeping.
var GatePollInterval = 5 * time.Second

// setGatePoll is the test seam for GatePollInterval.
func setGatePoll(d time.Duration) { GatePollInterval = d }

const (
	// GateBudget is how long one node has to come back after its upgrade.
	// Generous: it is restarting a database and rejoining a raft cluster.
	GateBudget = 5 * time.Minute

	// GateIndexLag is how far a node may trail the leader and still count as
	// caught up.
	GateIndexLag = 200
)

// ReadRoles asks every node what it is doing in the raft configuration.
//
// A node that cannot be reached, or whose answer cannot be parsed, is
// RoleUnknown — never a follower by default. Build then refuses to plan, which
// is the point: a rollout must not proceed past a node whose health is unknown.
func ReadRoles(nodes []inspector.Node, run SSHRunner) map[string]RaftRole {
	roles := make(map[string]RaftRole, len(nodes))
	for _, n := range nodes {
		st, err := observe(n, run)
		if err != nil {
			roles[n.Host] = RoleUnknown
			continue
		}
		switch strings.ToLower(st.RaftState) {
		case "leader":
			roles[n.Host] = RoleLeader
		case "follower":
			roles[n.Host] = RoleFollower
		default:
			// Candidate, Shutdown, or nothing. None of these is a follower.
			roles[n.Host] = RoleUnknown
		}
	}
	return roles
}

// WaitReady blocks until the node is carrying its share of the cluster again.
//
// This replaces a fixed `time.Sleep(--delay)` between nodes. A sleep cannot
// tell a node that rejoined in 20 seconds from one that never came back, so the
// rollout restarted the next voter either way — which is how a rolling upgrade
// takes out a quorum.
func WaitReady(node inspector.Node, run SSHRunner, budget time.Duration) error {
	if budget == 0 {
		budget = GateBudget
	}
	opts := nodehealth.Options{MaxIndexLag: GateIndexLag, RequireLeaderKnown: true}

	deadline := time.Now().Add(budget)
	var last error
	for {
		st, err := observe(node, run)
		if err == nil {
			last = st.Ready(opts)
			if last == nil {
				return nil
			}
		} else {
			last = err
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not come back within %s: %w", node.Host, budget, last)
		}
		time.Sleep(GatePollInterval)
	}
}

// probeCommand reads the node's own rqlite status and gateway health, on the
// node, so the operator's machine does not have to be on the overlay.
//
// The gateway probe prints its HTTP code rather than failing the command: a
// dead gateway is a health finding, not an unreachable node, and the two need
// different messages.
func probeCommand() string {
	return fmt.Sprintf(
		`printf '{"status":'; curl -fsS --max-time 5 http://localhost:%d/status; `+
			`printf ',"gateway_code":"'; `+
			`curl -s -o /dev/null -w '%%{http_code}' --max-time 5 http://localhost:%d/health; `+
			`printf '"}'`,
		constants.RQLiteHTTPPort, constants.GatewayAPIPort)
}

// observe runs one probe and turns it into a nodehealth.Status.
func observe(n inspector.Node, run SSHRunner) (nodehealth.Status, error) {
	out, err := run(n, probeCommand())
	if err != nil {
		return nodehealth.Status{}, fmt.Errorf("probe %s: %w", n.Host, err)
	}
	return parseProbe(out)
}

// parseProbe turns the probe's output into a Status.
func parseProbe(out string) (nodehealth.Status, error) {
	var probe struct {
		Status struct {
			Store struct {
				Raft struct {
					State        string `json:"state"`
					LeaderID     string `json:"leader_id"`
					AppliedIndex uint64 `json:"applied_index"`
					CommitIndex  uint64 `json:"commit_index"`
				} `json:"raft"`
			} `json:"store"`
		} `json:"status"`
		GatewayCode string `json:"gateway_code"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &probe); err != nil {
		return nodehealth.Status{}, fmt.Errorf("parse probe output %q: %w", truncate(out), err)
	}

	raft := probe.Status.Store.Raft
	return nodehealth.Status{
		RaftState:    raft.State,
		LeaderID:     raft.LeaderID,
		AppliedIndex: raft.AppliedIndex,
		CommitIndex:  raft.CommitIndex,
		GatewayOK:    probe.GatewayCode == "200",
	}, nil
}

// truncate keeps an unparseable probe from filling the terminal while still
// showing enough to recognise (an HTML error page, an SSH banner).
func truncate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
