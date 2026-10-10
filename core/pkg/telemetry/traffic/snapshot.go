package traffic

import (
	"math"
	"sort"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// Percentiles a snapshot reports.
const (
	p50 = 0.50
	p95 = 0.95
	p99 = 0.99
)

// aggregate is the window's buckets merged.
type aggregate struct {
	requests   int64
	errors4xx  int64
	errors5xx  int64
	bytes      int64
	latency    histogram
	namespaces map[string]*nsStats
}

// Snapshot reports the traffic of the WindowSeconds seconds up to now.
//
// Rates divide by the time the window actually covers: since the first
// request when the gateway has served for less than the window, and never
// less than one second. An empty recorder reports all zeros.
func (r *Recorder) Snapshot(now time.Time) report.TrafficReport {
	r.mu.Lock()
	defer r.mu.Unlock()

	cur := r.secondAt(now)
	agg := aggregate{namespaces: make(map[string]*nsStats)}
	for i := range r.ring {
		b := &r.ring[i]
		if b.sec < 0 || b.sec > cur || b.sec <= cur-WindowSeconds {
			continue
		}
		agg.add(b)
	}
	return agg.report(r.coveredSeconds(now), r.total)
}

// coveredSeconds is the span the window covers at now: the time since the
// first request, clamped to [1s, WindowSeconds]. Zero before any request.
func (r *Recorder) coveredSeconds(now time.Time) float64 {
	if !r.seen {
		return 0
	}
	covered := now.Sub(r.firstSeen).Seconds()
	return min(max(covered, 1), WindowSeconds)
}

func (a *aggregate) add(b *second) {
	a.requests += b.requests
	a.errors4xx += b.errors4xx
	a.errors5xx += b.errors5xx
	a.bytes += b.bytes
	a.latency.merge(&b.latency)
	for label, ns := range b.namespaces {
		dst, ok := a.namespaces[label]
		if !ok {
			dst = &nsStats{}
			a.namespaces[label] = dst
		}
		dst.requests += ns.requests
		dst.errors5xx += ns.errors5xx
		dst.latency.merge(&ns.latency)
	}
}

func (a *aggregate) report(covered float64, total int64) report.TrafficReport {
	out := report.TrafficReport{
		WindowSec:     int(math.Ceil(covered)),
		Requests:      a.requests,
		Errors5xx:     a.errors5xx,
		Errors4xx:     a.errors4xx,
		P50Ms:         a.latency.quantile(p50),
		P95Ms:         a.latency.quantile(p95),
		P99Ms:         a.latency.quantile(p99),
		TotalRequests: total,
		Namespaces:    a.topNamespaces(covered),
	}
	if a.requests > 0 {
		out.ErrorRate = float64(a.errors5xx) / float64(a.requests)
	}
	if covered > 0 {
		out.RPS = float64(a.requests) / covered
		out.BytesPerSec = float64(a.bytes) / covered
	}
	return out
}

// topNamespaces lists the TopNamespaces busiest namespaces, most requests
// first and by name among equals, so the order is stable between snapshots.
func (a *aggregate) topNamespaces(covered float64) []report.NamespaceTraffic {
	if len(a.namespaces) == 0 {
		return nil
	}
	out := make([]report.NamespaceTraffic, 0, len(a.namespaces))
	for label, ns := range a.namespaces {
		nt := report.NamespaceTraffic{
			Namespace: label,
			Requests:  ns.requests,
			Errors5xx: ns.errors5xx,
			P95Ms:     ns.latency.quantile(p95),
		}
		if covered > 0 {
			nt.RPS = float64(ns.requests) / covered
		}
		out = append(out, nt)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Namespace < out[j].Namespace
	})
	return out[:min(len(out), TopNamespaces)]
}
