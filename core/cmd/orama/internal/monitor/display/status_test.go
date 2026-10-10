package display

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/operatorview"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

type statusOut struct {
	Healthy bool `json:"healthy"`
	Verdict struct {
		State string `json:"state"`
	} `json:"verdict"`
	Nodes []struct {
		Host  string `json:"host"`
		Chain *struct {
			Height     int64 `json:"height"`
			CatchingUp bool  `json:"catching_up"`
			Validator  bool  `json:"validator"`
		} `json:"chain"`
	} `json:"nodes"`
	Operator *operatorview.Summary `json:"operator"`
}

func statusOf(t *testing.T, snap *cluster.ClusterSnapshot, op *operatorview.Summary) statusOut {
	t.Helper()
	var buf bytes.Buffer
	if err := StatusJSON(snap, op, &buf); err != nil {
		t.Fatal(err)
	}
	var out statusOut
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("%v: %s", err, buf.String())
	}
	return out
}

func withChains(snap *cluster.ClusterSnapshot) *cluster.ClusterSnapshot {
	for i := range snap.Nodes {
		snap.Nodes[i].Report.Chain = &report.ChainReport{Responsive: true, ChainID: "orama-stagenet-6",
			LatestHeight: 100, IsValidator: i == 0, VotingPower: 10}
	}
	return snap
}

// "healthy" is the one bit the newcomer acceptance test reads: true only when the verdict is
// operational, every node is healthy, and every chain answers and has caught up.
func TestStatusJSON_healthyOnlyWhenClusterAndEveryChainAreGood(t *testing.T) {
	good := statusOf(t, withChains(contractSnapshot()), nil)
	if good.Verdict.State != string(cluster.StateOperational) || !good.Healthy {
		t.Fatalf("a good cluster: %+v", good)
	}
	if len(good.Nodes) != 3 || good.Nodes[0].Chain == nil || !good.Nodes[0].Chain.Validator || good.Nodes[0].Chain.Height != 100 {
		t.Fatalf("nodes %+v", good.Nodes)
	}
	syncing := withChains(contractSnapshot())
	syncing.Nodes[1].Report.Chain.CatchingUp = true
	if statusOf(t, syncing, nil).Healthy {
		t.Error("a node still syncing its chain counted as healthy")
	}
	silent := withChains(contractSnapshot())
	silent.Nodes[2].Report.Chain.Responsive = false
	if statusOf(t, silent, nil).Healthy {
		t.Error("a node whose chain does not answer counted as healthy")
	}
	down := withChains(contractSnapshot())
	down.Nodes[2] = cluster.CollectionStatus{Node: cluster.NodeRef{Host: "3.3.3.3"}, Err: "SSH failed"}
	down.Alerts = cluster.DeriveAlerts(down)
	if statusOf(t, down, nil).Healthy {
		t.Error("an unreachable node counted as healthy")
	}
}

// A cluster-only node has no chain block, and is healthy on its cluster health alone. No nodes at
// all is never healthy.
func TestStatusJSON_clusterOnlyAndEmpty(t *testing.T) {
	out := statusOf(t, contractSnapshot(), nil)
	if out.Nodes[0].Chain != nil || out.Healthy != (out.Verdict.State == string(cluster.StateOperational)) {
		t.Fatalf("cluster-only: %+v", out)
	}
	if empty := statusOf(t, &cluster.ClusterSnapshot{}, nil); empty.Healthy || empty.Nodes == nil || len(empty.Nodes) != 0 {
		t.Fatalf("empty snapshot: %+v", empty)
	}
}

func TestStatusJSON_operatorOnlyWhenGiven(t *testing.T) {
	if statusOf(t, contractSnapshot(), nil).Operator != nil {
		t.Fatal("an operator section without --operator")
	}
	op := &operatorview.Summary{Address: "orama1x", Earnings: "5"}
	if got := statusOf(t, contractSnapshot(), op).Operator; got == nil || got.Earnings != "5" {
		t.Fatalf("operator %+v", got)
	}
}

func TestStatusTable_chainColumnVerdictAndOperator(t *testing.T) {
	snap := withChains(contractSnapshot())
	snap.Nodes[1].Report.Chain.CatchingUp = true
	snap.Nodes[2].Report.Chain = nil
	var buf bytes.Buffer
	op := &operatorview.Summary{Address: "orama1x", Earnings: "5", Spendable: "6", Bonded: "7", Err: "read the bonded of orama1x: 502"}
	if err := StatusTable(snap, op, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"CHAIN", "100 · validator", "syncing at 100", " - ", "nodes healthy ·",
		"Operator orama1x", "Earnings   5 norama", "Bonded     7 norama", "502"} {
		if !strings.Contains(out, want) {
			t.Errorf("status table misses %q:\n%s", want, out)
		}
	}
}
