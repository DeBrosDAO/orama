package rollout

import (
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

func node(host, role string) inspector.Node {
	return inspector.Node{Environment: "devnet", User: "orama", Host: host, Role: role}
}

func allFollowersExcept(leader string, hosts ...string) map[string]RaftRole {
	roles := map[string]RaftRole{}
	for _, h := range hosts {
		roles[h] = RoleFollower
	}
	roles[leader] = RoleLeader
	return roles
}

func hosts(p *Plan) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.Node.Host)
	}
	return out
}

// The property the whole package exists for. docs/DEV_DEPLOY.md claimed it;
// nothing implemented it.
func TestBuild_leader_goes_last(t *testing.T) {
	nodes := []inspector.Node{node("10.0.0.1", "node"), node("10.0.0.2", "node"), node("10.0.0.3", "node")}

	for _, leader := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		p, err := Build(nodes, allFollowersExcept(leader, "10.0.0.1", "10.0.0.2", "10.0.0.3"))
		if err != nil {
			t.Fatalf("leader %s: %v", leader, err)
		}
		last := p.Steps[len(p.Steps)-1]
		if last.Node.Host != leader {
			t.Fatalf("leader %s was not last; order was %v", leader, hosts(p))
		}
		if last.Role != RoleLeader {
			t.Fatalf("last step is not marked leader: %+v", last)
		}
		for _, s := range p.Steps[:len(p.Steps)-1] {
			if s.Role != RoleFollower {
				t.Fatalf("non-final step %s is %s, want Follower", s.Node.Host, s.Role)
			}
		}
	}
}

// No two nameservers back to back, so the zone always has one answering.
func TestBuild_nameservers_are_spaced(t *testing.T) {
	nodes := []inspector.Node{
		node("10.0.0.1", "nameserver-ns1"),
		node("10.0.0.2", "nameserver-ns2"),
		node("10.0.0.3", "node"),
		node("10.0.0.4", "node"),
		node("10.0.0.5", "node"),
	}
	p, err := Build(nodes, allFollowersExcept("10.0.0.5",
		"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4", "10.0.0.5"))
	if err != nil {
		t.Fatal(err)
	}

	for i := 1; i < len(p.Steps); i++ {
		if p.Steps[i].IsNameserver && p.Steps[i-1].IsNameserver {
			t.Fatalf("two nameservers restart back to back: %v", hosts(p))
		}
	}
	if p.Nameservers != 2 {
		t.Fatalf("counted %d nameservers, want 2", p.Nameservers)
	}
}

// The current 3+3 topology: every node is a nameserver, so there is nothing to
// interleave with. Ordering must still be stable and leader-last, since each
// node is gated on the previous one anyway.
func TestBuild_all_nameservers_is_stable_and_leader_last(t *testing.T) {
	nodes := []inspector.Node{
		node("10.0.0.3", "nameserver-ns3"),
		node("10.0.0.1", "nameserver-ns1"),
		node("10.0.0.2", "nameserver-ns2"),
	}
	p, err := Build(nodes, allFollowersExcept("10.0.0.2", "10.0.0.1", "10.0.0.2", "10.0.0.3"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.1", "10.0.0.3", "10.0.0.2"}
	got := hosts(p)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// No leader means no quorum. A rolling upgrade against a cluster that cannot
// commit a write is the last thing that should start.
func TestBuild_no_leader_refuses(t *testing.T) {
	nodes := []inspector.Node{node("10.0.0.1", "node"), node("10.0.0.2", "node")}
	roles := map[string]RaftRole{"10.0.0.1": RoleFollower, "10.0.0.2": RoleFollower}

	_, err := Build(nodes, roles)
	var noLeader *ErrNoLeader
	if !errors.As(err, &noLeader) {
		t.Fatalf("want ErrNoLeader, got %v", err)
	}
	if !strings.Contains(err.Error(), "quorum") {
		t.Fatalf("error does not explain the consequence: %v", err)
	}
}

// An unreachable node is not a healthy follower. Assuming it is one is how a
// rollout removes the second-to-last voter.
func TestBuild_unreachable_node_refuses(t *testing.T) {
	nodes := []inspector.Node{node("10.0.0.1", "node"), node("10.0.0.2", "node"), node("10.0.0.3", "node")}
	roles := map[string]RaftRole{
		"10.0.0.1": RoleLeader,
		"10.0.0.2": RoleFollower,
		"10.0.0.3": RoleUnknown,
	}

	_, err := Build(nodes, roles)
	var unreachable *ErrUnreachable
	if !errors.As(err, &unreachable) {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}
	if !strings.Contains(err.Error(), "10.0.0.3") {
		t.Fatalf("error does not name the unreachable node: %v", err)
	}
}

// A missing entry is as unknown as an explicit RoleUnknown — a role map that
// forgot a node must not silently make it a follower.
func TestBuild_missing_role_entry_refuses(t *testing.T) {
	nodes := []inspector.Node{node("10.0.0.1", "node"), node("10.0.0.2", "node")}
	roles := map[string]RaftRole{"10.0.0.1": RoleLeader}

	if _, err := Build(nodes, roles); err == nil {
		t.Fatal("a node absent from the role map was accepted")
	}
}

// Two leaders means the reads straddled an election: the plan would be built on
// a state that no longer exists.
func TestBuild_two_leaders_refuses(t *testing.T) {
	nodes := []inspector.Node{node("10.0.0.1", "node"), node("10.0.0.2", "node")}
	roles := map[string]RaftRole{"10.0.0.1": RoleLeader, "10.0.0.2": RoleLeader}

	_, err := Build(nodes, roles)
	if err == nil {
		t.Fatal("two leaders produced a plan")
	}
	if !strings.Contains(err.Error(), "mid-election") {
		t.Fatalf("error does not explain the situation: %v", err)
	}
}

func TestBuild_single_node_is_just_the_leader(t *testing.T) {
	nodes := []inspector.Node{node("10.0.0.1", "node")}
	p, err := Build(nodes, map[string]RaftRole{"10.0.0.1": RoleLeader})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 1 || p.Steps[0].Role != RoleLeader {
		t.Fatalf("unexpected plan: %+v", p.Steps)
	}
}

// A plan printed twice must be the same plan, or an operator cannot approve it.
func TestBuild_is_deterministic(t *testing.T) {
	nodes := []inspector.Node{node("10.0.0.9", "node"), node("10.0.0.2", "nameserver-ns1"), node("10.0.0.5", "node")}
	roles := allFollowersExcept("10.0.0.5", "10.0.0.9", "10.0.0.2", "10.0.0.5")

	first, err := Build(nodes, roles)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Build(nodes, roles)
		if err != nil {
			t.Fatal(err)
		}
		for j := range first.Steps {
			if first.Steps[j].Node.Host != again.Steps[j].Node.Host {
				t.Fatalf("run %d differs: %v vs %v", i, hosts(first), hosts(again))
			}
		}
	}
}

func TestPlan_String_shows_every_node_and_its_reason(t *testing.T) {
	nodes := []inspector.Node{node("10.0.0.1", "nameserver-ns1"), node("10.0.0.2", "node")}
	p, err := Build(nodes, allFollowersExcept("10.0.0.2", "10.0.0.1", "10.0.0.2"))
	if err != nil {
		t.Fatal(err)
	}

	out := p.String()
	for _, want := range []string{"10.0.0.1", "10.0.0.2", "leader", "follower"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan output missing %q:\n%s", want, out)
		}
	}
}
