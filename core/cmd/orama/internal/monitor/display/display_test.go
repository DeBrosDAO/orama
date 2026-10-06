package display

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// Every table leads with the verdict, and output to anything but a terminal
// carries no escape codes.
func TestClusterTable_everyTableLeadsWithTheVerdictWithoutANSI(t *testing.T) {
	tables := map[string]func(*cluster.ClusterSnapshot, *bytes.Buffer) error{
		"cluster":    func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return ClusterTable(s, b) },
		"node":       func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return NodeTable(s, b) },
		"service":    func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return ServiceTable(s, b) },
		"mesh":       func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return MeshTable(s, b) },
		"dns":        func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return DNSTable(s, b) },
		"namespaces": func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return NamespacesTable(s, b) },
		"alerts":     func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return AlertsTable(s, b) },
		"traffic":    func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return TrafficTable(s, b) },
		"chain":      func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return ChainTable(s, b) },
	}
	snap := contractSnapshot()
	snap.Nodes = append(snap.Nodes, cluster.CollectionStatus{Node: cluster.NodeRef{Host: "4.4.4.4", Role: "node"}, Err: "SSH failed"})
	snap.Alerts = cluster.DeriveAlerts(snap)
	for name, render := range tables {
		var buf bytes.Buffer
		if err := render(snap, &buf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := buf.String()
		if !strings.HasPrefix(out, "✗ ") {
			t.Errorf("%s does not start with the verdict: %q", name, strings.SplitN(out, "\n", 2)[0])
		}
		if strings.Contains(out, "\x1b") {
			t.Errorf("%s wrote ANSI codes to a non-terminal", name)
		}
	}
}

func TestClusterTable_listsComponentsNodesAndHints(t *testing.T) {
	snap := contractSnapshot()
	snap.Nodes[2] = cluster.CollectionStatus{Node: cluster.NodeRef{Host: "3.3.3.3"}, Err: "SSH failed (exit 255)"}
	snap.Alerts = cluster.DeriveAlerts(snap)
	var buf bytes.Buffer
	if err := ClusterTable(snap, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"API Gateway", "3.3.3.3 unreachable: SSH failed", "Top alerts",
		"→ orama monitor node --env devnet --node 3.3.3.3 --ssh"} {
		if !strings.Contains(out, want) {
			t.Errorf("cluster table is missing %q:\n%s", want, out)
		}
	}
}

func TestAlertLines_limitAndDuplicates(t *testing.T) {
	alerts := []cluster.Alert{
		{Severity: cluster.AlertWarning, Subsystem: "dns", Node: "1.1.1.1", Message: "cert"},
		{Severity: cluster.AlertWarning, Subsystem: "dns", Node: "1.1.1.1", Message: "cert"},
		{Severity: cluster.AlertInfo, Subsystem: "system", Node: "1.1.1.1", Message: "zombies"},
	}
	out := AlertLines(view.NewTheme(false), view.PrepareAlerts(alerts, view.FilterAll), "devnet", 1)
	if !strings.Contains(out, "cert (×2)") || !strings.Contains(out, "… 1 more: orama monitor alerts --env devnet") {
		t.Fatalf("got:\n%s", out)
	}
	if strings.Contains(out, "zombies") {
		t.Fatalf("the limit was not applied:\n%s", out)
	}
}

func TestTrafficJSON_shape(t *testing.T) {
	snap := contractSnapshot()
	snap.Nodes[0].Report.Traffic = &report.TrafficReport{RPS: 4, Requests: 100, Errors5xx: 1, P95Ms: 20}
	var buf bytes.Buffer
	if err := TrafficJSON(snap, &buf); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Totals struct {
			RPS       float64 `json:"rps"`
			ErrorRate float64 `json:"error_rate"`
			Reporting int     `json:"reporting_gateways"`
		} `json:"totals"`
		Nodes      []map[string]any `json:"nodes"`
		Namespaces []any            `json:"namespaces"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Totals.RPS != 4 || got.Totals.ErrorRate != 0.01 || got.Totals.Reporting != 1 || len(got.Nodes) != 3 || got.Namespaces == nil {
		t.Fatalf("got %+v", got)
	}
}

func TestTrafficTables_noTraffic(t *testing.T) {
	out := TrafficTables(view.NewTheme(false), view.AggregateTraffic(contractSnapshot()))
	if !strings.Contains(out, "No gateway reported traffic") {
		t.Fatalf("got %q", out)
	}
}

func TestChainJSON_shape(t *testing.T) {
	snap := contractSnapshot()
	snap.Nodes[0].Report.Chain = &report.ChainReport{Responsive: true, ChainID: "orama-1", LatestHeight: 42,
		Validators: []report.ChainValidator{{Address: "A", VotingPower: 1}}, TotalVotingPower: 1}
	var buf bytes.Buffer
	if err := ChainJSON(snap, &buf); err != nil {
		t.Fatal(err)
	}
	var got struct {
		ChainID    string           `json:"chain_id"`
		Height     int64            `json:"height"`
		Nodes      []map[string]any `json:"nodes"`
		Validators []struct {
			SharePct float64 `json:"share_pct"`
		} `json:"validators"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ChainID != "orama-1" || got.Height != 42 || len(got.Nodes) != 1 || got.Validators[0].SharePct != 100 {
		t.Fatalf("got %+v", got)
	}
}

func TestChainTables_noChain(t *testing.T) {
	if out := ChainTables(view.NewTheme(false), view.PrepareChain(contractSnapshot())); !strings.Contains(out, "No node reports a chain") {
		t.Fatalf("got %q", out)
	}
}

func TestClusterJSON_everyJSONViewOfAnEmptySnapshotIsAnEmptyList(t *testing.T) {
	writers := map[string]func(*cluster.ClusterSnapshot, *bytes.Buffer) error{
		"cluster":    func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return ClusterJSON(s, b) },
		"mesh":       func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return MeshJSON(s, b) },
		"dns":        func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return DNSJSON(s, b) },
		"namespaces": func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return NamespacesJSON(s, b) },
		"service":    func(s *cluster.ClusterSnapshot, b *bytes.Buffer) error { return ServiceJSON(s, b) },
	}
	for name, write := range writers {
		var buf bytes.Buffer
		if err := write(&cluster.ClusterSnapshot{}, &buf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.TrimSpace(buf.String()) != "[]" {
			t.Errorf("%s: %q, want []", name, buf.String())
		}
	}
}

// A node keeps its peers while one of them is down, so the expected peer
// count is over every member, not over the nodes that reported.
func TestMeshNodes_expectsPeersOverEveryMember(t *testing.T) {
	snap := contractSnapshot()
	snap.Nodes = append(snap.Nodes, cluster.CollectionStatus{Node: cluster.NodeRef{Host: "4.4.4.4"}, Err: "down"})
	for i := range 3 {
		snap.Nodes[i].Report.WireGuard.PeerCount = 3
	}
	out := MeshNodes(view.NewTheme(false), snap)
	if strings.Count(out, "3/3") != 3 {
		t.Fatalf("correctly peered nodes shown as mismatched:\n%s", out)
	}
}
