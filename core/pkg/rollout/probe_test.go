package rollout

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func probeOutput(state, leader string, applied, commit uint64, gatewayCode string) string {
	return fmt.Sprintf(
		`{"status":{"store":{"raft":{"state":%q,"leader_id":%q,"applied_index":%d,"commit_index":%d}}},"gateway_code":%q}`,
		state, leader, applied, commit, gatewayCode)
}

func TestParseProbe_reads_every_field(t *testing.T) {
	got, err := parseProbe(probeOutput("Leader", "n1", 7, 9, "200"))
	if err != nil {
		t.Fatal(err)
	}
	if got.RaftState != "Leader" || got.LeaderID != "n1" || got.AppliedIndex != 7 || got.CommitIndex != 9 {
		t.Fatalf("got %+v", got)
	}
	if !got.GatewayOK {
		t.Fatal("a 200 from /health was not read as healthy")
	}
}

// A dead gateway must be a health finding, not a parse failure — the two need
// different messages and different operator actions.
func TestParseProbe_non_200_gateway_is_unhealthy_not_an_error(t *testing.T) {
	got, err := parseProbe(probeOutput("Follower", "n1", 5, 5, "503"))
	if err != nil {
		t.Fatalf("a dead gateway was reported as a broken probe: %v", err)
	}
	if got.GatewayOK {
		t.Fatal("a 503 was read as healthy")
	}
	if got.RaftState != "Follower" {
		t.Fatalf("raft state lost: %+v", got)
	}
}

// curl failing entirely leaves "000" — still a parseable probe of an unhealthy
// gateway.
func TestParseProbe_curl_failure_code(t *testing.T) {
	got, err := parseProbe(probeOutput("Follower", "n1", 5, 5, "000"))
	if err != nil {
		t.Fatal(err)
	}
	if got.GatewayOK {
		t.Fatal(`"000" was read as healthy`)
	}
}

func TestParseProbe_garbage_is_an_error_that_shows_the_output(t *testing.T) {
	_, err := parseProbe("bash: curl: command not found")
	if err == nil {
		t.Fatal("garbage parsed successfully")
	}
	if !strings.Contains(err.Error(), "command not found") {
		t.Fatalf("error does not show what came back: %v", err)
	}
}

func TestParseProbe_truncates_a_huge_body(t *testing.T) {
	_, err := parseProbe(strings.Repeat("x", 5000))
	if err == nil {
		t.Fatal("want an error")
	}
	if len(err.Error()) > 400 {
		t.Fatalf("error is %d chars; it should be truncated", len(err.Error()))
	}
}

func TestReadRoles_classifies_each_node(t *testing.T) {
	nodes := []inspector.Node{
		{Host: "10.0.0.1"}, {Host: "10.0.0.2"}, {Host: "10.0.0.3"}, {Host: "10.0.0.4"},
	}
	run := func(n inspector.Node, _ string) (string, error) {
		switch n.Host {
		case "10.0.0.1":
			return probeOutput("Leader", "n1", 9, 9, "200"), nil
		case "10.0.0.2":
			return probeOutput("Follower", "n1", 9, 9, "200"), nil
		case "10.0.0.3":
			return probeOutput("Candidate", "", 0, 0, "000"), nil
		default:
			return "", errors.New("connection refused")
		}
	}

	roles := ReadRoles(nodes, run)
	want := map[string]RaftRole{
		"10.0.0.1": RoleLeader,
		"10.0.0.2": RoleFollower,
		"10.0.0.3": RoleUnknown, // Candidate is not a follower
		"10.0.0.4": RoleUnknown, // unreachable is not a follower
	}
	for host, w := range want {
		if roles[host] != w {
			t.Errorf("%s: got %s, want %s", host, roles[host], w)
		}
	}
}

// The gate must wait for the node to actually rejoin — the property a fixed
// sleep could not provide.
func TestWaitReady_waits_for_the_node_to_rejoin(t *testing.T) {
	original := GatePollInterval
	defer func() { setGatePoll(original) }()
	setGatePoll(0)

	calls := 0
	run := func(inspector.Node, string) (string, error) {
		calls++
		if calls < 4 {
			return probeOutput("Candidate", "", 0, 0, "000"), nil
		}
		return probeOutput("Follower", "n1", 100, 100, "200"), nil
	}

	if err := WaitReady(inspector.Node{Host: "10.0.0.2"}, run, time.Minute); err != nil {
		t.Fatal(err)
	}
	if calls < 4 {
		t.Fatalf("returned after %d probes; it should have waited", calls)
	}
}

// A node that never comes back must stop the rollout, and the error must say
// what state it was stuck in.
func TestWaitReady_stuck_node_fails_with_the_reason(t *testing.T) {
	original := GatePollInterval
	defer func() { setGatePoll(original) }()
	setGatePoll(0)

	run := func(inspector.Node, string) (string, error) {
		return probeOutput("Candidate", "", 0, 0, "000"), nil
	}

	err := WaitReady(inspector.Node{Host: "10.0.0.7"}, run, time.Millisecond)
	if err == nil {
		t.Fatal("a node stuck in Candidate passed the gate")
	}
	if !strings.Contains(err.Error(), "10.0.0.7") || !strings.Contains(err.Error(), "Candidate") {
		t.Fatalf("error names neither the node nor the state: %v", err)
	}
}

// A node whose raft is fine but whose gateway never comes back must not pass:
// it serves no traffic, and the next voter must not be restarted behind it.
func TestWaitReady_raft_healthy_but_gateway_dead_fails(t *testing.T) {
	original := GatePollInterval
	defer func() { setGatePoll(original) }()
	setGatePoll(0)

	run := func(inspector.Node, string) (string, error) {
		return probeOutput("Follower", "n1", 50, 50, "502"), nil
	}

	err := WaitReady(inspector.Node{Host: "10.0.0.3"}, run, time.Millisecond)
	if err == nil {
		t.Fatal("a node with a dead gateway passed the gate")
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("error does not name the gateway: %v", err)
	}
}

// An unreachable node must not pass the gate either — SSH failing is not
// evidence the node is healthy.
func TestWaitReady_unreachable_node_fails(t *testing.T) {
	original := GatePollInterval
	defer func() { setGatePoll(original) }()
	setGatePoll(0)

	run := func(inspector.Node, string) (string, error) {
		return "", errors.New("ssh: connect to host 10.0.0.8 port 22: Connection refused")
	}

	err := WaitReady(inspector.Node{Host: "10.0.0.8"}, run, time.Millisecond)
	if err == nil {
		t.Fatal("an unreachable node passed the gate")
	}
	if !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("error loses the cause: %v", err)
	}
}
