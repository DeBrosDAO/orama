package upgrade

import (
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rollout"
)

// A three-voter cluster; 37.59.116.212 is the stagenet node from the bug.
var stagenet = []inspector.Node{
	{Environment: "stagenet", Host: "37.59.116.212", Role: "nameserver-ns1"},
	{Environment: "stagenet", Host: "203.0.113.2", Role: "nameserver-ns2"},
	{Environment: "stagenet", Host: "203.0.113.3", Role: "nameserver-ns3"},
}

func rolesWithLeader(leader string) map[string]rollout.RaftRole {
	roles := map[string]rollout.RaftRole{}
	for _, n := range stagenet {
		roles[n.Host] = rollout.RoleFollower
	}
	roles[leader] = rollout.RoleLeader
	return roles
}

// Bugboard 2721: `--node 37.59.116.212` on a healthy cluster read only that
// follower, found no leader, and refused with "the cluster has no quorum".
func TestPlanRollout_filteredFollowerInHealthyCluster(t *testing.T) {
	plan, err := planRollout(stagenet, rolesWithLeader("203.0.113.2"), "37.59.116.212")
	if err != nil {
		t.Fatalf("a healthy cluster must plan a single follower: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("steps = %d, want 1: %s", len(plan.Steps), plan)
	}
	if s := plan.Steps[0]; s.Node.Host != "37.59.116.212" || s.Role != rollout.RoleFollower {
		t.Fatalf("step = %+v, want the named follower", s)
	}
}

// Naming the leader keeps its leader step; the node-side upgrade hands
// leadership over before it stops anything.
func TestPlanRollout_filteredLeaderKeepsLeaderStep(t *testing.T) {
	plan, err := planRollout(stagenet, rolesWithLeader("37.59.116.212"), "37.59.116.212")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("steps = %d, want 1: %s", len(plan.Steps), plan)
	}
	s := plan.Steps[0]
	if s.Role != rollout.RoleLeader || !strings.Contains(s.Reason, "leadership transfer") {
		t.Fatalf("step = %+v, want the leader step with its leadership transfer", s)
	}
}

// The preconditions are about the cluster, so a filter does not relax them.
func TestPlanRollout_filterStillRefusesWithoutLeader(t *testing.T) {
	roles := map[string]rollout.RaftRole{}
	for _, n := range stagenet {
		roles[n.Host] = rollout.RoleFollower
	}
	_, err := planRollout(stagenet, roles, "37.59.116.212")
	var noLeader *rollout.ErrNoLeader
	if !errors.As(err, &noLeader) {
		t.Fatalf("err = %v, want ErrNoLeader", err)
	}
}

// Another node being unreadable is still a refusal: the named node's restart
// may be the one that takes quorum away.
func TestPlanRollout_filterStillRefusesWhenAnotherNodeIsUnreadable(t *testing.T) {
	roles := rolesWithLeader("203.0.113.2")
	roles["203.0.113.3"] = rollout.RoleUnknown
	_, err := planRollout(stagenet, roles, "37.59.116.212")
	var unreachable *rollout.ErrUnreachable
	if !errors.As(err, &unreachable) {
		t.Fatalf("err = %v, want ErrUnreachable", err)
	}
	if !strings.Contains(err.Error(), "203.0.113.3") {
		t.Fatalf("err = %v, want it to name the unreadable node", err)
	}
}

func TestPlanRollout_unknownNodeErrors(t *testing.T) {
	_, err := planRollout(stagenet, rolesWithLeader("203.0.113.2"), "10.9.9.9")
	if err == nil || !strings.Contains(err.Error(), "10.9.9.9") {
		t.Fatalf("err = %v, want an error naming the unknown node", err)
	}
}

func TestPlanRollout_noFilterPlansEveryNode(t *testing.T) {
	plan, err := planRollout(stagenet, rolesWithLeader("203.0.113.2"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != len(stagenet) {
		t.Fatalf("steps = %d, want %d", len(plan.Steps), len(stagenet))
	}
	if last := plan.Steps[len(plan.Steps)-1]; last.Node.Host != "203.0.113.2" {
		t.Fatalf("leader %s is not last: %s", "203.0.113.2", plan)
	}
}
