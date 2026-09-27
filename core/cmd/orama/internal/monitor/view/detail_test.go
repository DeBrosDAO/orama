package view

import (
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

func sectionByTitle(sections []Section, title string) *Section {
	for i := range sections {
		if sections[i].Title == title {
			return &sections[i]
		}
	}
	return nil
}

func fieldValue(s *Section, key string) (string, bool) {
	for _, f := range s.Fields {
		if f[0] == key {
			return f[1], true
		}
	}
	return "", false
}

func TestReportSections_everyPresentSectionInOrder(t *testing.T) {
	r := healthyStatus("1.1.1.1", true).Report
	r.Timestamp = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	r.Errors = []string{"a", "b"}
	r.Chain = &report.ChainReport{ChainID: "orama-1"}
	sections := ReportSections(r)

	var titles []string
	for _, s := range sections {
		titles = append(titles, s.Title)
	}
	want := "node,rqlite,olric,ipfs,vault,gateway,wireguard,chain"
	if strings.Join(titles, ",") != want {
		t.Fatalf("sections = %v, want %s (nil sections left out)", titles, want)
	}
	top := sectionByTitle(sections, "node")
	if v, _ := fieldValue(top, "timestamp"); v != "2026-09-27T12:00:00Z" {
		t.Errorf("timestamp = %q", v)
	}
	if v, _ := fieldValue(top, "errors"); v != "a, b" {
		t.Errorf("errors = %q", v)
	}
	if v, _ := fieldValue(sectionByTitle(sections, "rqlite"), "responsive"); v != "yes" {
		t.Errorf("rqlite responsive = %q", v)
	}
}

func TestReportSections_listsBecomeTables(t *testing.T) {
	r := &report.NodeReport{
		Services: &report.ServicesReport{Services: []report.ServiceInfo{{Name: "orama-node", ActiveState: "active", NRestarts: 2}}},
		RQLite: &report.RQLiteReport{
			Nodes:     map[string]report.RQLiteNodeInfo{"b": {Reachable: true}, "a": {Leader: true}},
			DebugVars: &report.RQLiteDebugVarsReport{QueryErrors: 4},
		},
		Namespaces: []report.NamespaceReport{{Name: "anchat", RQLiteUp: true}},
	}
	sections := ReportSections(r)

	svc := sectionByTitle(sections, "services")
	if len(svc.Tables) != 1 || svc.Tables[0].Headers[0] != "name" || svc.Tables[0].Rows[0][0] != "orama-node" {
		t.Fatalf("services table = %+v", svc.Tables)
	}
	rq := sectionByTitle(sections, "rqlite")
	if len(rq.Tables) != 1 || rq.Tables[0].Headers[0] != "key" || rq.Tables[0].Rows[0][0] != "a" {
		t.Fatalf("rqlite nodes table = %+v (want keys sorted)", rq.Tables)
	}
	if v, ok := fieldValue(rq, "debug_vars.query_errors"); !ok || v != "4" {
		t.Fatalf("nested struct not flattened: %q %v", v, ok)
	}
	ns := sectionByTitle(sections, "namespaces")
	if ns == nil || ns.Tables[0].Rows[0][0] != "anchat" {
		t.Fatalf("namespaces section = %+v", ns)
	}
}

func TestReportSections_nilReport(t *testing.T) {
	if got := ReportSections(nil); got != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestNodeCells_unreachableAndReported(t *testing.T) {
	th := NewTheme(false)
	down := NodeCells(th, cluster.CollectionStatus{Node: cluster.NodeRef{Host: "9.9.9.9"}, Err: "timeout"})
	if len(down) != len(NodeHeaders) || down[1] != "node" || down[2] != "unreachable" {
		t.Fatalf("unreachable row = %v", down)
	}
	up := healthyStatus("1.1.1.1", true)
	up.Report.System = &report.SystemReport{LoadAvg1: 0.5, MemUsePct: 40, DiskUsePct: 91}
	up.Report.Version = "0.200.0"
	up.ReportAgeSec = 4
	cells := NodeCells(th, up)
	want := []string{"1.1.1.1", "node", "healthy", "Leader", "OK", "0.50", "40%", "91%", "0.200.0", "4s"}
	if strings.Join(cells, "|") != strings.Join(want, "|") {
		t.Fatalf("row = %v\nwant  %v", cells, want)
	}
}
