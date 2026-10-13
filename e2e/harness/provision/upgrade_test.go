package provision

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

func TestUp_installsThePreviousRelease(t *testing.T) {
	e := newTestEnv(t)
	e.cfg.PreviousArchive = previousRefPrefix + "v0.200.0"
	e.cfg.InstallPrevious = true
	st, err := up(context.Background(), e.cfg, &testLogger{}, e.d)
	if err != nil {
		t.Fatalf("up installing the previous release: %v", err)
	}
	setups := 0
	for _, c := range e.cmd.calls {
		if len(c.args) < 2 || c.args[0] != "node" || c.args[1] != "setup" {
			continue
		}
		setups++
		if c.name != st.PreviousOramaBin || !strings.Contains(c.String(), "--archive "+st.PreviousArchivePath) {
			t.Fatalf("`%s` did not install the previous archive with the previous CLI", c.String())
		}
	}
	if setups != nodeCount {
		t.Fatalf("%d node setups, want %d", setups, nodeCount)
	}
}

func TestPlan_namesThePreviousReleaseInstall(t *testing.T) {
	e := newTestEnv(t)
	e.cfg.PreviousArchive, e.cfg.InstallPrevious = previousRefPrefix+"v0.200.0", true
	actions, err := Plan(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	var genesis string
	for _, a := range actions {
		if a.Phase == "genesis" {
			genesis = a.Description
		}
	}
	if !strings.Contains(genesis, "orama-prev") || !strings.Contains(genesis, "previous archive") {
		t.Fatalf("genesis plan %q", genesis)
	}
}

func TestUpgradeToHead_followersFirstLeaderLastWithGates(t *testing.T) {
	e, st := upForTest(t)
	from := len(e.cmd.lines())
	if err := upgradeToHead(context.Background(), st, &testLogger{}, e.cmd, fastTiming()); err != nil {
		t.Fatalf("upgradeToHead: %v", err)
	}
	leader := st.Nodes[0].PublicIP // the fake report names 203.0.113.3, node-1
	var want []string
	want = append(want, "maint push --env e2e-testrun1 --archive "+st.ArchivePath, "status report --env e2e-testrun1 --json")
	for _, ip := range []string{st.Nodes[1].PublicIP, st.Nodes[2].PublicIP, leader} {
		want = append(want, "node upgrade --env e2e-testrun1 --node "+ip+" --yes",
			"status report --env e2e-testrun1 --node "+ip+" --json", "status report --env e2e-testrun1 --json")
	}
	got := e.cmd.lines()[from:]
	if len(got) != len(want) {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i := range want {
		if !strings.HasSuffix(got[i], want[i]) {
			t.Fatalf("call %d is %q, want %q", i, got[i], want[i])
		}
	}
	for _, c := range e.cmd.calls[from:] {
		if c.name != st.OramaBin || !containsAll(c.env, e2eFlag, "RW_AGENT_SOCK="+st.RWSock, "HOME="+st.Home) {
			t.Fatalf("`%s` ran without the HEAD CLI and the test agent: %v", c.String(), c.env)
		}
	}
}

func TestUpgradeToHead_stopsAtTheFirstFailedNode(t *testing.T) {
	e, st := upForTest(t)
	e.cmd.fail["--node "+st.Nodes[1].PublicIP+" --yes"] = 1
	err := upgradeToHead(context.Background(), st, &testLogger{}, e.cmd, fastTiming())
	if err == nil || !strings.Contains(err.Error(), "the upgrade of node-2 failed") {
		t.Fatalf("upgrade with node-2 failing: %v", err)
	}
	for _, l := range e.cmd.lines() {
		if strings.Contains(l, "node upgrade") && !strings.Contains(l, st.Nodes[1].PublicIP) {
			t.Fatalf("a node after the failed one was upgraded: %s", l)
		}
	}
}

func TestUpgradeToHead_unhealthyClusterIsNotTouched(t *testing.T) {
	e, st := upForTest(t)
	e.cmd.healthy = false
	err := upgradeToHead(context.Background(), st, &testLogger{}, e.cmd, fastTiming())
	if err == nil || !strings.Contains(err.Error(), "not upgrading anything") {
		t.Fatalf("upgrade of an unhealthy cluster: %v", err)
	}
	assertNoUpgrade(t, e)
}

func TestUpgradeToHead_unknownLeaderIsRefused(t *testing.T) {
	e, st := upForTest(t)
	e.cmd.leader = "198.51.100.9"
	err := upgradeToHead(context.Background(), st, &testLogger{}, e.cmd, fastTiming())
	if err == nil || !strings.Contains(err.Error(), "not a node of this run") {
		t.Fatalf("upgrade with a foreign leader: %v", err)
	}
	assertNoUpgrade(t, e)
}

func TestUpgradeToHead_refusesAnIncompleteState(t *testing.T) {
	e := newTestEnv(t)
	for _, st := range []*fleet.State{nil, {RunID: "testrun1"}, {RunID: "Bad Id"}} {
		if err := upgradeToHead(context.Background(), st, &testLogger{}, e.cmd, fastTiming()); err == nil {
			t.Errorf("upgradeToHead(%+v) went ahead", st)
		}
	}
	if len(e.cmd.lines()) != 0 {
		t.Fatal("a refused upgrade ran the CLI")
	}
}

func assertNoUpgrade(t *testing.T, e *testEnv) {
	t.Helper()
	for _, l := range e.cmd.lines() {
		if strings.Contains(l, "node upgrade") {
			t.Fatalf("a node was upgraded: %s", l)
		}
	}
}

func TestUpgradeOrder(t *testing.T) {
	nodes := []fleet.Node{{Name: "a", PublicIP: "203.0.113.1", WGIP: "10.0.0.1"}, {Name: "b", PublicIP: "203.0.113.2", WGIP: "10.0.0.2"}}
	order, err := upgradeOrder(nodes, "10.0.0.1")
	if err != nil || order[0].Name != "b" || order[1].Name != "a" {
		t.Fatalf("order by WireGuard leader: %+v %v", order, err)
	}
	if _, err := upgradeOrder(nodes, noLeader); err == nil {
		t.Fatal("an order without a leader was accepted")
	}
}
