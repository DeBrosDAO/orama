package monitor

import (
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/cli/production/report"
	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// `orama status` used to carry its own copy of this classification. Two
// commands reading the same cluster could call the same node healthy and
// degraded at once. These pin the one definition down.

func node(gatewayUp bool, raft string) CollectionStatus {
	return CollectionStatus{
		Node: inspector.Node{Host: "10.0.0.1", Role: "node"},
		Report: &report.NodeReport{
			Gateway: &report.GatewayReport{Responsive: gatewayUp},
			RQLite:  &report.RQLiteReport{RaftState: raft},
		},
	}
}

func TestHealth_healthy_needs_gateway_and_settled_raft(t *testing.T) {
	for _, raft := range []string{"Leader", "Follower"} {
		if got := node(true, raft).Health(); got != HealthHealthy {
			t.Errorf("gateway up + raft %s = %s, want %s", raft, got, HealthHealthy)
		}
	}
}

func TestHealth_degraded_when_gateway_down(t *testing.T) {
	cs := node(false, "Leader")
	if got := cs.Health(); got != HealthDegraded {
		t.Fatalf("Health() = %s, want %s", got, HealthDegraded)
	}
	if !strings.Contains(cs.Detail(), "gateway down") {
		t.Errorf("Detail() = %q, want it to name the gateway", cs.Detail())
	}
}

func TestHealth_degraded_when_raft_unsettled(t *testing.T) {
	cs := node(true, "Candidate")
	if got := cs.Health(); got != HealthDegraded {
		t.Fatalf("Health() = %s, want %s", got, HealthDegraded)
	}
	if !strings.Contains(cs.Detail(), "Candidate") {
		t.Errorf("Detail() = %q, want it to name the raft state", cs.Detail())
	}
}

func TestHealth_unsettled_raft_names_unknown_when_state_is_empty(t *testing.T) {
	cs := node(true, "")
	if got := cs.Detail(); !strings.Contains(got, "unknown") {
		t.Errorf("Detail() = %q, want %q for an empty raft state", got, "raft unknown")
	}
}

func TestHealth_unreachable_when_ssh_failed(t *testing.T) {
	cs := CollectionStatus{
		Node:  inspector.Node{Host: "10.0.0.1"},
		Error: errors.New("SSH failed (exit 255)"),
	}
	if got := cs.Health(); got != HealthUnreachable {
		t.Fatalf("Health() = %s, want %s", got, HealthUnreachable)
	}
	if cs.Detail() != "SSH failed (exit 255)" {
		t.Errorf("Detail() = %q, want the SSH error", cs.Detail())
	}
}

// A node that answered SSH but returned nothing parseable is not "degraded":
// nothing is known about it.
func TestHealth_unreachable_when_report_is_nil(t *testing.T) {
	cs := CollectionStatus{Node: inspector.Node{Host: "10.0.0.1"}}
	if got := cs.Health(); got != HealthUnreachable {
		t.Fatalf("Health() = %s, want %s", got, HealthUnreachable)
	}
	if cs.Detail() == "" {
		t.Error("Detail() is empty; an unreachable node must say why")
	}
}

// Missing subsystem reports are nil pointers, not zero structs.
func TestHealth_nil_subsystem_reports_do_not_panic(t *testing.T) {
	cs := CollectionStatus{
		Node:   inspector.Node{Host: "10.0.0.1"},
		Report: &report.NodeReport{},
	}
	if got := cs.Health(); got != HealthDegraded {
		t.Fatalf("Health() = %s, want %s", got, HealthDegraded)
	}
	detail := cs.Detail()
	if !strings.Contains(detail, "gateway down") || !strings.Contains(detail, "rqlite") {
		t.Errorf("Detail() = %q, want it to name both missing subsystems", detail)
	}
}

func TestHealth_healthy_node_has_no_detail(t *testing.T) {
	if got := node(true, "Leader").Detail(); got != "" {
		t.Errorf("Detail() = %q, want empty for a healthy node", got)
	}
}
