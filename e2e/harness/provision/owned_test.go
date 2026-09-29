package provision

import (
	"context"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
)

// TestDestroyNode_refusesAServerOfAnotherRun: a state whose server id
// points at another run's server (stale or edited) must not delete it.
func TestDestroyNode_refusesAServerOfAnotherRun(t *testing.T) {
	e, st := upForTest(t)
	foreign := e.cloud.addServer("e2e-other001-n1", map[string]string{hetzner.LabelRun: "other001"}, st2time())
	st.Nodes[1].ServerID = foreign.ID
	err := destroyNode(context.Background(), st, st.Nodes[1].PublicIP, e.d)
	if err == nil || !strings.Contains(err.Error(), "refusing to delete server") {
		t.Fatalf("err %v", err)
	}
	if _, err := e.cloud.GetServer(context.Background(), foreign.ID); err != nil {
		t.Fatal("the other run's server was deleted")
	}
}

// TestRemoveExtra_refusesAServerWithAnotherName: an extra whose server id
// names a server of the run under another name is not deleted.
func TestRemoveExtra_refusesAServerWithAnotherName(t *testing.T) {
	e, st := upForTest(t)
	if _, err := addExtra(context.Background(), st, "extra-1", "nbg1", testLimits, e.d); err != nil {
		t.Fatal(err)
	}
	st.Extras[0].Name = "extra-9"
	if err := removeExtra(context.Background(), st, "extra-9", e.d); err == nil || !strings.Contains(err.Error(), "e2e-testrun1-extra-9") {
		t.Fatalf("err %v", err)
	}
	if _, err := e.cloud.GetServer(context.Background(), st.Extras[0].ServerID); err != nil {
		t.Fatal("a server of another name was deleted")
	}
}

// TestAddExtra_cancelledBootStillDeletesTheServer: when the caller's
// context ends while the extra boots, the half-made server is deleted on a
// context of its own instead of leaking until the sweep.
func TestAddExtra_cancelledBootStillDeletesTheServer(t *testing.T) {
	e, st := upForTest(t)
	e.cloud.ctxAware = true
	before, _, _ := e.cloud.counts()
	e.remote.scanErr = context.Canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := addExtra(ctx, st, "extra-1", "nbg1", testLimits, e.d); err == nil {
		t.Fatal("addExtra succeeded")
	}
	if after, _, _ := e.cloud.counts(); after != before {
		t.Fatalf("servers %d -> %d: the cancelled extra leaked", before, after)
	}
}

// TestDown_failsWhenSomethingIsLeft: a delete that answered success but
// left the resource makes Down fail (the final listing by label sees it).
func TestDown_failsWhenSomethingIsLeft(t *testing.T) {
	e, st := upForTest(t)
	e.cloud.keysSurviveDelete = true
	if err := down(context.Background(), st, &testLogger{}, e.d); err == nil || !strings.Contains(err.Error(), "left 0 servers, 0 firewalls and 1 SSH keys") {
		t.Fatalf("err %v", err)
	}
}
