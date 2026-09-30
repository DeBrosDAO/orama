package provision

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/broker"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

const evalSub = namePrefix + "testrun1-evalx." + testZone

func TestAddEvalCluster_installsDelegatesAndRecords(t *testing.T) {
	e, st := upForTest(t)
	cl, err := addEvalCluster(context.Background(), st, "evalx", testLimits, &testLogger{}, e.d)
	if err != nil {
		t.Fatal(err)
	}
	if cl.Env != namePrefix+"testrun1-evalx" || cl.BaseDomain != evalSub || cl.GatewayURL != "https://"+evalSub ||
		cl.Node.Role != fleet.RoleNameserver || !strings.HasPrefix(cl.HostKey, "SHA256:") {
		t.Fatalf("cluster %+v", cl)
	}
	if raw, err := os.ReadFile(cl.CAFile); err != nil || len(raw) == 0 {
		t.Fatalf("CA file: %v", err)
	}
	if !e.dns.has("NS", evalSub, "ns1."+evalSub) || !e.dns.has("A", "ns1."+evalSub, cl.Node.PublicIP) {
		t.Fatalf("delegation missing: %+v", e.dns.records)
	}
	calls := strings.Join(e.cmd.lines(), "\n")
	for _, want := range []string{"env add " + cl.Env + " https://" + evalSub, "--ca-file " + cl.CAFile,
		"node setup --ip " + cl.Node.PublicIP, "--genesis", "--role nameserver", "--base-domain " + evalSub} {
		if !strings.Contains(calls, want) {
			t.Errorf("no %q in\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "env use "+cl.Env) {
		t.Error("the eval environment was made the current one of the shared HOME")
	}
}

func TestAddEvalCluster_refusals(t *testing.T) {
	e, st := upForTest(t)
	for _, name := range []string{"", "Eval", "a-b", "1abc", strings.Repeat("a", 13)} {
		if _, err := addEvalCluster(context.Background(), st, name, testLimits, &testLogger{}, e.d); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}
	if _, err := addEvalCluster(context.Background(), &fleet.State{RunID: "testrun1"}, "evalx", testLimits, &testLogger{}, e.d); err == nil {
		t.Error("a run that is not up was accepted")
	}
	e.dns.add("TXT", "x."+evalSub, "left", st2time())
	if _, err := addEvalCluster(context.Background(), st, "evalx", testLimits, &testLogger{}, e.d); err == nil ||
		!strings.Contains(err.Error(), "already has 1 DNS records (first: TXT x."+evalSub) {
		t.Errorf("a subdomain with records left was reused or misreported: %v", err)
	}
}

func TestAddEvalCluster_failedGenesisRemovesEverything(t *testing.T) {
	e, st := upForTest(t)
	servers, _, _ := e.cloud.counts()
	e.cmd.fail["--base-domain "+evalSub] = 1
	if _, err := addEvalCluster(context.Background(), st, "evalx", testLimits, &testLogger{}, e.d); err == nil {
		t.Fatal("a failed genesis was reported up")
	}
	if after, _, _ := e.cloud.counts(); after != servers {
		t.Fatalf("servers %d -> %d: the eval server leaked", servers, after)
	}
	if left, _ := e.dns.ListUnder(context.Background(), evalSub); len(left) != 0 {
		t.Fatalf("records left: %+v", left)
	}
	if !strings.Contains(strings.Join(e.cmd.lines(), "\n"), "env remove "+namePrefix+"testrun1-evalx") {
		t.Fatal("the environment was not removed")
	}
}

func TestRemoveEvalCluster_idempotent(t *testing.T) {
	e, st := upForTest(t)
	cl, err := addEvalCluster(context.Background(), st, "evalx", testLimits, &testLogger{}, e.d)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := removeEvalCluster(context.Background(), st, "evalx", &testLogger{}, e.d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(cl.CAFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CA file still there: %v", err)
	}
}

func TestDown_deletesClusterSubdomainRecords(t *testing.T) {
	e, st := upForTest(t)
	if _, err := addEvalCluster(context.Background(), st, "evalx", testLimits, &testLogger{}, e.d); err != nil {
		t.Fatal(err)
	}
	e.dns.add("TXT", "_orama-verify.app."+namePrefix+"testrun1-cd."+testZone, "tok", st2time())
	e.dns.add("TXT", "x."+namePrefix+"otherrun-cd."+testZone, "keep", st2time())
	if err := down(context.Background(), st, &testLogger{}, e.d); err != nil {
		t.Fatal(err)
	}
	recs, _ := e.dns.RunRecords(context.Background())
	if len(recs) != 1 || !strings.Contains(recs[0].Name, "otherrun") {
		t.Fatalf("records after teardown: %+v", recs)
	}
}

// TestAddExtra_featureProcessUsesTheBroker: with E2E_BROKER_SOCK set the
// public functions never read a token; they ask the broker.
func TestAddExtra_featureProcessUsesTheBroker(t *testing.T) {
	cloud := &recordingCloud{}
	sock := startTestBroker(t, cloud)
	t.Setenv(broker.EnvSock, sock)
	t.Setenv("HCLOUD_TOKEN", "")
	st := &fleet.State{RunID: "testrun1"}
	n, err := AddExtra(context.Background(), st, "extra-1", "hel1")
	if err != nil || n.Name != "extra-1" || len(st.Extras) != 1 {
		t.Fatalf("node %+v err %v extras %v", n, err, st.Extras)
	}
	if err := DestroyNode(context.Background(), st, n.PublicIP); err != nil || len(st.Extras) != 0 {
		t.Fatalf("destroy: %v, extras %v", err, st.Extras)
	}
	st.Nodes = []fleet.Node{{Name: "node-1", PublicIP: "203.0.113.1"}}
	if err := DestroyNode(context.Background(), st, "203.0.113.1"); err == nil || !strings.Contains(err.Error(), "outside any feature process") {
		t.Fatal("a feature process destroyed a core node")
	}
	if _, err := AddEvalCluster(context.Background(), st, "evalx"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveEvalCluster(context.Background(), st, "evalx"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(cloud.ops, ",") != "add extra-1,remove extra-1,cluster evalx,uncluster evalx" {
		t.Fatalf("ops %v", cloud.ops)
	}
}
