//go:build e2e_fleet

package monitoring

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/monitor"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// Request metrics (docs/MONITORING.md "Request metrics").
const (
	invalidLabel     = "(invalid)"
	trafficWindowSec = 60
	// maxNamespaceLabel is the longest namespace name counted as itself.
	maxNamespaceLabel = 64
	// trafficBurst is how many requests make a namespace one of the busiest.
	trafficBurst = 30
	// collectEvery is the self-collection period; a report may lag it by a
	// hung collection (docs/MONITORING.md "Self collection").
	collectEvery = 10 * time.Second
	collectSlack = 15 * time.Second
)

type trafficView struct {
	Totals struct {
		Reporting int `json:"reporting_gateways"`
	} `json:"totals"`
	Nodes []struct {
		Host    string `json:"host"`
		Traffic *struct {
			WindowSec int     `json:"window_sec"`
			Requests  int64   `json:"requests"`
			P50Ms     float64 `json:"p50_ms"`
			P95Ms     float64 `json:"p95_ms"`
			P99Ms     float64 `json:"p99_ms"`
			ErrorRate float64 `json:"error_rate"`
		} `json:"traffic"`
	} `json:"nodes"`
	Namespaces []struct {
		Namespace string `json:"namespace"`
		Requests  int64  `json:"requests"`
	} `json:"namespaces"`
}

// TestTraffic_namespacesAttributedAndInvalidFolded: requests to a namespace
// host are counted against that namespace, and a host naming an invalid
// namespace is counted as "(invalid)" rather than as its text; every gateway
// reports a window of at most 60s with ordered percentiles
// (docs/MONITORING.md "Request metrics").
func TestTraffic_namespacesAttributedAndInvalidFolded(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	n := tenancy.Namespace(t, f, ns.Options{})
	bogus := "ns-" + strings.Repeat("x", maxNamespaceLabel+6) + "." + f.State.BaseDomain
	c := harness.GW(t)
	for range trafficBurst {
		c.MustSend(t, gw.Req{Path: "/v1/version", Host: bogus})
		tenancy.Post(t, n.Client, "/v1/rqlite/query", tenancy.Owner(n), map[string]any{"sql": "SELECT 1", "args": []any{}})
	}
	eventually.Require(t, edge.PollEvery, collectEvery+collectSlack+time.Minute, "both labels in monitor traffic", func() (bool, error) {
		var v trafficView
		monitorJSON(t, "traffic", &v)
		labels := []string{}
		for _, ns := range v.Namespaces {
			labels = append(labels, ns.Namespace)
		}
		if slices.Contains(labels, invalidLabel) && slices.Contains(labels, n.Name) {
			return true, nil
		}
		return false, fmt.Errorf("busiest namespaces %v", labels)
	})
	var v trafficView
	monitorJSON(t, "traffic", &v)
	for _, row := range v.Nodes {
		tr := row.Traffic
		if tr == nil || tr.WindowSec < 1 || tr.WindowSec > trafficWindowSec || tr.P50Ms > tr.P95Ms || tr.P95Ms > tr.P99Ms || tr.ErrorRate < 0 || tr.ErrorRate > 1 {
			t.Errorf("%s traffic %+v", row.Host, tr)
		}
	}
	if v.Totals.Reporting != len(f.State.Nodes) {
		t.Errorf("%d gateways report traffic, want %d", v.Totals.Reporting, len(f.State.Nodes))
	}
}

// TestTelemetry_reportsRefreshEveryTenSeconds: each node's report timestamp
// advances about every 10s and no report is older than the freshness bound
// (docs/MONITORING.md "Self collection", "report_age_sec").
func TestTelemetry_reportsRefreshEveryTenSeconds(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	seen := map[string][]time.Time{}
	eventually.Require(t, 2*time.Second, 2*time.Minute, "three fresh reports from every node", func() (bool, error) {
		r, err := monitor.Get(t.Context(), harness.CLI(t), f.State.Env)
		if err != nil {
			return false, err
		}
		for _, n := range r.Nodes {
			if n.Report == nil || n.ReportAgeSec > monitor.MaxReportAgeSec {
				return false, eventually.Stop(fmt.Errorf("%s: report missing or %ds old", n.Host, n.ReportAgeSec))
			}
			if ts := seen[n.Host]; len(ts) == 0 || !ts[len(ts)-1].Equal(n.Report.Timestamp) {
				seen[n.Host] = append(ts, n.Report.Timestamp)
			}
		}
		for _, n := range f.State.Nodes {
			if len(seen[n.PublicIP]) < 3 {
				return false, fmt.Errorf("%s: %d distinct reports so far", n.Name, len(seen[n.PublicIP]))
			}
		}
		return true, nil
	})
	for host, ts := range seen {
		for i := 1; i < len(ts); i++ {
			if gap := ts[i].Sub(ts[i-1]); gap > collectEvery+collectSlack {
				t.Errorf("%s: %s between two reports, want about %s", host, gap, collectEvery)
			}
		}
	}
}
