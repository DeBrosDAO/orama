package display

import (
	"io"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor/view"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// systemd ActiveState values the service matrix tells apart.
const (
	serviceActive   = "active"
	serviceInactive = "inactive"
)

// ServiceTable prints a service-by-node matrix of systemd states.
func ServiceTable(snap *cluster.ClusterSnapshot, w io.Writer) error {
	t := view.ThemeFor(w)
	var b strings.Builder
	writeHeader(&b, t, snap, "Services")
	b.WriteString("\n")
	b.WriteString(ServiceMatrix(t, snap))
	return flush(w, &b)
}

// ServiceMatrix is a row per service and a column per reporting node, then
// any failed units, or a note that there is no service data.
func ServiceMatrix(t view.Theme, snap *cluster.ClusterSnapshot) string {
	nodes := reported(snap)
	names := map[string]bool{}
	states := map[string]map[string]report.ServiceInfo{}
	for _, cs := range nodes {
		states[cs.Node.Host] = map[string]report.ServiceInfo{}
		if cs.Report.Services == nil {
			continue
		}
		for _, svc := range cs.Report.Services.Services {
			names[svc.Name] = true
			states[cs.Node.Host][svc.Name] = svc
		}
	}
	if len(names) == 0 {
		return tableIndent + t.Muted.Render("No service data available") + "\n"
	}
	headers := []string{"SERVICE"}
	for _, cs := range nodes {
		headers = append(headers, cs.Node.Host)
	}
	var rows [][]string
	for _, name := range sortedKeys(names) {
		row := []string{name}
		for _, cs := range nodes {
			svc, ok := states[cs.Node.Host][name]
			row = append(row, serviceCell(t, svc, ok))
		}
		rows = append(rows, row)
	}
	return view.Table(t, tableIndent, headers, rows) + failedUnits(t, nodes)
}

func serviceCell(t view.Theme, svc report.ServiceInfo, ok bool) string {
	switch {
	case !ok:
		return t.Muted.Render("--")
	case svc.RestartLoopRisk:
		return t.Crit.Render("RESTART-LOOP")
	case svc.ActiveState == serviceActive:
		return t.OK.Render(svc.ActiveState)
	case svc.ActiveState == unitFailed:
		return t.Crit.Render("FAILED")
	case svc.ActiveState == serviceInactive:
		return t.Muted.Render(svc.ActiveState)
	default:
		return t.Warn.Render(svc.ActiveState)
	}
}

func failedUnits(t view.Theme, nodes []cluster.CollectionStatus) string {
	var b strings.Builder
	for _, cs := range nodes {
		if cs.Report.Services == nil || len(cs.Report.Services.FailedUnits) == 0 {
			continue
		}
		b.WriteString(tableIndent + t.Bold.Render(cs.Node.Host+" failed units: ") +
			t.Crit.Render(strings.Join(cs.Report.Services.FailedUnits, ", ")) + "\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n" + b.String()
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func activeServices(svcs []report.ServiceInfo) int {
	n := 0
	for _, s := range svcs {
		if s.ActiveState == serviceActive {
			n++
		}
	}
	return n
}

// ServiceJSON writes each node's service states as JSON.
func ServiceJSON(snap *cluster.ClusterSnapshot, w io.Writer) error {
	type svcEntry struct {
		Host     string            `json:"host"`
		Services map[string]string `json:"services"`
	}

	entries := []svcEntry{}
	for _, cs := range reported(snap) {
		if cs.Report.Services == nil {
			continue
		}
		e := svcEntry{Host: cs.Node.Host, Services: map[string]string{}}
		for _, svc := range cs.Report.Services.Services {
			e.Services[svc.Name] = svc.ActiveState
		}
		entries = append(entries, e)
	}
	return writeJSON(w, entries)
}
