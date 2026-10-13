package monitor

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

func TestWithReport_takesTheOverlayAddressFromTheReport(t *testing.T) {
	cs := withReport(cluster.CollectionStatus{Node: cluster.NodeRef{Host: "1.1.1.1"}}, "1.1.1.1",
		`{"hostname":"","wireguard_ip":"10.0.0.7","version":"0.200.0"}`)
	if cs.Err != "" || cs.Node.WGIP != "10.0.0.7" || cs.Report.PublicIP != "1.1.1.1" || cs.Report.Hostname != "1.1.1.1" {
		t.Fatalf("got %+v / %+v", cs.Node, cs.Report)
	}
	if got, err := FilterNode(&cluster.ClusterSnapshot{Nodes: []cluster.CollectionStatus{cs}}, "10.0.0.7"); err != nil || len(got.Nodes) != 1 {
		t.Fatalf("the overlay address did not select the node: %v", err)
	}
}

func TestWithReport_garbageIsANodeError(t *testing.T) {
	cs := withReport(cluster.CollectionStatus{}, "1.1.1.1", "sudo: orama: command not found")
	if cs.Report != nil || !strings.Contains(cs.Err, "parse report JSON") || !strings.Contains(cs.Err, "command not found") {
		t.Fatalf("got %+v", cs)
	}
}
