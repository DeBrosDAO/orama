package view

import (
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// TopNamespaceRows is how many namespaces the traffic views list.
const TopNamespaceRows = 10

// NodeTraffic is one node's gateway traffic. Traffic is nil when the node
// reported none (it serves no requests, or its report did not come back).
type NodeTraffic struct {
	Host    string
	Traffic *report.TrafficReport
}

// TrafficTotals is the cluster's traffic summed over the nodes that reported.
// ErrorRate is 5xx over requests, weighted by each node's requests. The
// percentiles are the worst node's: an upper bound, since per-node summaries
// cannot be merged into an exact cluster percentile.
type TrafficTotals struct {
	Reporting int
	RPS       float64
	Requests  int64
	Errors5xx int64
	Errors4xx int64
	ErrorRate float64
	P50Ms     float64
	P95Ms     float64
	P99Ms     float64
}

// TrafficSummary is what the traffic views show.
type TrafficSummary struct {
	Nodes      []NodeTraffic
	Totals     TrafficTotals
	Namespaces []report.NamespaceTraffic
}

// AggregateTraffic sums the snapshot's per-node traffic.
func AggregateTraffic(snap *cluster.ClusterSnapshot) TrafficSummary {
	var s TrafficSummary
	ns := map[string]*report.NamespaceTraffic{}
	for _, n := range snap.Nodes {
		row := NodeTraffic{Host: n.Node.Host}
		if n.Report != nil && n.Report.Traffic != nil {
			row.Traffic = n.Report.Traffic
			addTraffic(&s.Totals, n.Report.Traffic)
			mergeNamespaces(ns, n.Report.Traffic.Namespaces)
		}
		s.Nodes = append(s.Nodes, row)
	}
	if s.Totals.Requests > 0 {
		s.Totals.ErrorRate = float64(s.Totals.Errors5xx) / float64(s.Totals.Requests)
	}
	s.Namespaces = topNamespaces(ns, TopNamespaceRows)
	return s
}

func addTraffic(t *TrafficTotals, tr *report.TrafficReport) {
	t.Reporting++
	t.RPS += tr.RPS
	t.Requests += tr.Requests
	t.Errors5xx += tr.Errors5xx
	t.Errors4xx += tr.Errors4xx
	t.P50Ms = max(t.P50Ms, tr.P50Ms)
	t.P95Ms = max(t.P95Ms, tr.P95Ms)
	t.P99Ms = max(t.P99Ms, tr.P99Ms)
}

func mergeNamespaces(into map[string]*report.NamespaceTraffic, from []report.NamespaceTraffic) {
	for _, n := range from {
		dst, ok := into[n.Namespace]
		if !ok {
			dst = &report.NamespaceTraffic{Namespace: n.Namespace}
			into[n.Namespace] = dst
		}
		dst.Requests += n.Requests
		dst.RPS += n.RPS
		dst.Errors5xx += n.Errors5xx
		dst.P95Ms = max(dst.P95Ms, n.P95Ms)
	}
}

// topNamespaces is the busiest namespaces, most requests first and by name
// among equals, so the order holds still between refreshes.
func topNamespaces(ns map[string]*report.NamespaceTraffic, limit int) []report.NamespaceTraffic {
	out := make([]report.NamespaceTraffic, 0, len(ns))
	for _, n := range ns {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Namespace < out[j].Namespace
	})
	return out[:min(len(out), limit)]
}

// Series keeps the last N values of a measurement, oldest first.
type Series struct {
	limit  int
	values []float64
}

// NewSeries keeps at most limit values.
func NewSeries(limit int) *Series {
	return &Series{limit: limit}
}

// Push appends v, dropping the oldest value once the series is full.
func (s *Series) Push(v float64) {
	s.values = append(s.values, v)
	if len(s.values) > s.limit {
		s.values = s.values[len(s.values)-s.limit:]
	}
}

// Values is the kept values, oldest first.
func (s *Series) Values() []float64 {
	return s.values
}

// sparkLevels are the eight block heights a sparkline draws with.
var sparkLevels = []rune("▁▂▃▄▅▆▇█")

// Sparkline draws values as block characters scaled to their maximum. An
// all-zero series draws as a flat baseline rather than nothing.
func Sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	peak := 0.0
	for _, v := range values {
		peak = max(peak, v)
	}
	var b strings.Builder
	for _, v := range values {
		level := 0
		if peak > 0 && v > 0 {
			level = int(v / peak * float64(len(sparkLevels)-1))
		}
		b.WriteRune(sparkLevels[level])
	}
	return b.String()
}
