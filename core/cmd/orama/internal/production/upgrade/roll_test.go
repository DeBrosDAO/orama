package upgrade

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rollout"
)

// walk is a RemoteUpgrader whose steps and gate write to a log.
type walk struct {
	up  *RemoteUpgrader
	log []string
}

func newWalk(flags *Flags) *walk {
	w := &walk{up: NewRemoteUpgrader(flags)}
	w.up.step = func(n inspector.Node) error { w.log = append(w.log, "upgrade "+n.Host); return nil }
	w.up.gate = func(n inspector.Node, _ time.Duration) error { w.log = append(w.log, "gate "+n.Host); return nil }
	w.up.AfterUpgrade = func(n inspector.Node) error { w.log = append(w.log, "after "+n.Host); return nil }
	return w
}

func planOf(hosts ...string) *rollout.Plan {
	p := &rollout.Plan{}
	for _, h := range hosts {
		p.Steps = append(p.Steps, rollout.Step{Node: inspector.Node{Host: h}, Role: rollout.RoleFollower})
	}
	return p
}

func TestRoll_aNodesHookRunsBetweenItsUpgradeAndItsGateAndNeverTwoNodesAtOnce(t *testing.T) {
	w := newWalk(&Flags{})

	if err := w.up.Roll(planOf("a", "b", "c")); err != nil {
		t.Fatalf("Roll: %v", err)
	}

	want := []string{"upgrade a", "after a", "gate a", "upgrade b", "after b", "gate b", "upgrade c", "after c"}
	if !slices.Equal(w.log, want) {
		t.Errorf("log = %v\nwant  %v: one node at a time, the hook before the gate, and no gate after the last node", w.log, want)
	}
}

func TestRoll_aFailingHookStopsTheRolloutBeforeTheGateAndTheNextNode(t *testing.T) {
	w := newWalk(&Flags{})
	w.up.AfterUpgrade = func(n inspector.Node) error {
		w.log = append(w.log, "after "+n.Host)
		return errors.New("the global layer was not refreshed")
	}

	err := w.up.Roll(planOf("a", "b", "c"))

	if err == nil || !strings.Contains(err.Error(), "global layer was not refreshed") || !strings.Contains(err.Error(), "2 node(s) not upgraded") {
		t.Fatalf("err = %v, want the hook's failure and the count of nodes left", err)
	}
	if want := []string{"upgrade a", "after a"}; !slices.Equal(w.log, want) {
		t.Errorf("log = %v, want %v: nothing after the failing hook", w.log, want)
	}
}

func TestRoll_aFailingUpgradeNeverReachesTheHook(t *testing.T) {
	w := newWalk(&Flags{})
	w.up.step = func(n inspector.Node) error {
		w.log = append(w.log, "upgrade "+n.Host)
		return errors.New("preflight refused")
	}

	err := w.up.Roll(planOf("a", "b"))

	if err == nil || !strings.Contains(err.Error(), "upgrade failed on a") {
		t.Fatalf("err = %v", err)
	}
	if want := []string{"upgrade a"}; !slices.Equal(w.log, want) {
		t.Errorf("log = %v, want %v", w.log, want)
	}
}

func TestRoll_aNodeThatDoesNotRejoinStopsTheRollout(t *testing.T) {
	w := newWalk(&Flags{})
	w.up.gate = func(n inspector.Node, _ time.Duration) error {
		w.log = append(w.log, "gate "+n.Host)
		return errors.New("never rejoined")
	}

	err := w.up.Roll(planOf("a", "b"))

	if err == nil || !strings.Contains(err.Error(), "never rejoined") || !strings.Contains(err.Error(), "remaining voters") {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(w.log, "upgrade b") {
		t.Errorf("log = %v: the next node was restarted though the last one had not rejoined", w.log)
	}
}

func TestRoll_withoutAHookThePlanRollsAsBefore(t *testing.T) {
	w := newWalk(&Flags{})
	w.up.AfterUpgrade = nil

	if err := w.up.Roll(planOf("a", "b")); err != nil {
		t.Fatalf("Roll: %v", err)
	}

	if want := []string{"upgrade a", "gate a", "upgrade b"}; !slices.Equal(w.log, want) {
		t.Errorf("log = %v, want %v", w.log, want)
	}
}

func TestRoll_theGateGetsTheDelayAsItsBudget(t *testing.T) {
	var got time.Duration
	w := newWalk(&Flags{Delay: 42})
	w.up.gate = func(_ inspector.Node, budget time.Duration) error { got = budget; return nil }

	if err := w.up.Roll(planOf("a", "b")); err != nil {
		t.Fatalf("Roll: %v", err)
	}

	if got != 42*time.Second {
		t.Errorf("gate budget = %s, want 42s", got)
	}
}
