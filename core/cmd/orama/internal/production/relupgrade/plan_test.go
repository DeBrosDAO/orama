package relupgrade

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/rollout"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name            string
		current, target string
		reinstall       bool
		want            Action
	}{
		{"older upgrades", "0.3.0", "0.4.0", false, ActionUpgrade},
		{"same is current", "0.4.0", "0.4.0", false, ActionCurrent},
		{"same with reinstall", "0.4.0", "0.4.0", true, ActionReinstall},
		{"newer is left alone", "0.5.0", "0.4.0", false, ActionAhead},
		{"newer is left alone even with reinstall", "0.5.0", "0.4.0", true, ActionAhead},
		{"unknown version upgrades", "", "0.4.0", false, ActionUpgrade},
		{"a build that cannot be ordered upgrades", "dev", "0.4.0", false, ActionUpgrade},
		{"a v prefix is ignored", "v0.4.0", "0.4.0", false, ActionCurrent},
		{"minor beats patch", "0.10.0", "0.9.9", false, ActionAhead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.current, tt.target, tt.reinstall); got != tt.want {
				t.Errorf("Classify(%q, %q, %v) = %s, want %s", tt.current, tt.target, tt.reinstall, got, tt.want)
			}
		})
	}
}

func steps(hosts ...string) []rollout.Step {
	var out []rollout.Step
	for i, h := range hosts {
		role := rollout.RoleFollower
		if i == len(hosts)-1 {
			role = rollout.RoleLeader
		}
		out = append(out, rollout.Step{Node: inspector.Node{Host: h}, Role: role, IsNameserver: i == 0})
	}
	return out
}

func TestBuildPlan_keepsTheRolloutOrderAndMarksNodes(t *testing.T) {
	order := steps("a", "b", "c")
	states := map[string]NodeState{"a": {Version: "0.3.0"}, "b": {Version: "0.4.0"}, "c": {Version: "0.3.0", Global: true}}

	p := BuildPlan(testTarget(), "0.4.0", order, states, false)

	var hosts, actions []string
	for _, r := range p.Rows {
		hosts = append(hosts, r.Host)
		actions = append(actions, string(r.Action))
	}
	if strings.Join(hosts, ",") != "a,b,c" || strings.Join(actions, ",") != "upgrade,current,upgrade" {
		t.Errorf("rows = %v %v", hosts, actions)
	}
	if p.Runs() != 2 {
		t.Errorf("Runs = %d, want 2", p.Runs())
	}
	got := p.Steps(order)
	if len(got) != 2 || got[0].Node.Host != "a" || got[1].Node.Host != "c" {
		t.Errorf("Steps = %v, want a then c in rollout order", got)
	}
}

func TestBuildPlan_noReportMeansUnknownVersionAndUpgrade(t *testing.T) {
	p := BuildPlan(testTarget(), "0.4.0", steps("a"), map[string]NodeState{}, false)

	if p.Rows[0].Action != ActionUpgrade || p.Rows[0].Current != "" {
		t.Errorf("row = %+v", p.Rows[0])
	}
	var out bytes.Buffer
	p.Render(&out)
	if !strings.Contains(out.String(), "unknown") {
		t.Errorf("a node with no report is not shown as unknown:\n%s", &out)
	}
}

func TestPlan_Render(t *testing.T) {
	p := BuildPlan(testTarget(), "0.4.0", steps("a", "b"), map[string]NodeState{"a": {Version: "0.3.0", Global: true}, "b": {Version: "0.5.0"}}, false)
	var out bytes.Buffer

	p.Render(&out)

	for _, want := range []string{"stagenet", "nightly", "0.4.0", "refreshed after the node", "b runs 0.5.0, newer than the channel's 0.4.0: left alone."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the plan lacks %q:\n%s", want, &out)
		}
	}
}

func TestPlan_SummaryNamesWhatHappensNext(t *testing.T) {
	none := BuildPlan(testTarget(), "0.4.0", steps("a"), map[string]NodeState{"a": {Version: "0.4.0"}}, false)
	if !strings.Contains(none.Summary(), "nothing to do") || !strings.Contains(none.Summary(), "--reinstall") {
		t.Errorf("summary = %q", none.Summary())
	}
	some := BuildPlan(testTarget(), "0.4.0", steps("a", "b"), map[string]NodeState{"a": {Version: "0.3.0"}}, false)
	if !strings.Contains(some.Summary(), "2 of 2") || !strings.Contains(some.Summary(), "one at a time") {
		t.Errorf("summary = %q", some.Summary())
	}
}

func TestStatesFromSnapshot(t *testing.T) {
	snap := &cluster.ClusterSnapshot{Nodes: []cluster.CollectionStatus{
		{Node: cluster.NodeRef{Host: "a"}, Report: &report.NodeReport{Version: "v0.3.0", Global: &report.GlobalReport{Units: []report.GlobalUnit{{Name: "orama-global-chain.service"}}}}},
		{Node: cluster.NodeRef{Host: "b"}, Report: &report.NodeReport{Version: "0.4.0", Global: &report.GlobalReport{}}},
		{Node: cluster.NodeRef{Host: "c"}, Err: "timeout"},
	}}

	got := statesFromSnapshot(snap)

	if got["a"] != (NodeState{Version: "0.3.0", Global: true}) || got["b"] != (NodeState{Version: "0.4.0"}) {
		t.Errorf("states = %+v", got)
	}
	if _, ok := got["c"]; ok {
		t.Error("a node that did not report must be absent, not an empty version")
	}
}
