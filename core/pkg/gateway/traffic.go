package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

// trafficExcludedPaths and trafficExcludedPrefixes (matched as whole path
// segments: the prefix itself and everything below it) are the gateway's own
// health and telemetry plumbing. They are polled by the monitor and by the
// other nodes, so counting them would make the request metrics measure the
// monitoring itself.
var (
	trafficExcludedPaths = map[string]bool{
		"/health":           true,
		"/v1/health":        true,
		"/v1/internal/ping": true,
	}
	trafficExcludedPrefixes = []string{
		"/v1/internal/telemetry",
		"/v1/operator/telemetry",
	}
)

// trafficAttributionKey carries a request's *trafficAttribution.
type trafficAttributionKey struct{}

// trafficAttribution is where the middlewares below loggingMiddleware note
// the namespace a request served. loggingMiddleware reads it after the
// handler chain returns: a context value set further in is not visible out
// there, so it hands the chain this slot instead. Every write happens on the
// serving goroutine before the chain returns.
type trafficAttribution struct {
	namespace string
}

func withTrafficAttribution(r *http.Request) (*http.Request, *trafficAttribution) {
	a := &trafficAttribution{}
	return r.WithContext(context.WithValue(r.Context(), trafficAttributionKey{}, a)), a
}

// markTrafficNamespace attributes r to namespace in the request metrics.
func markTrafficNamespace(r *http.Request, namespace string) {
	if a, ok := r.Context().Value(trafficAttributionKey{}).(*trafficAttribution); ok && namespace != "" {
		a.namespace = namespace
	}
}

// trafficAttributionMiddleware attributes a request to the namespace auth or
// domain routing resolved for it. It sits directly inside authMiddleware, so
// requests refused by authorization, scopes or namespace rate limits are
// attributed too.
func (g *Gateway) trafficAttributionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ns, ok := r.Context().Value(CtxKeyNamespaceOverride).(string); ok {
			markTrafficNamespace(r, ns)
		}
		next.ServeHTTP(w, r)
	})
}

func trafficExcluded(path string) bool {
	if trafficExcludedPaths[path] {
		return true
	}
	for _, p := range trafficExcludedPrefixes {
		if strings.HasPrefix(path, p) && (len(path) == len(p) || path[len(p)] == '/') {
			return true
		}
	}
	return false
}

// recordTraffic adds a finished request to the request metrics. A request no
// middleware attributed counts against this gateway's own namespace. A
// WebSocket upgrade counts without a latency sample: its duration is the
// life of the connection.
func (g *Gateway) recordTraffic(r *http.Request, a *trafficAttribution, status, bytes int, dur time.Duration) {
	if g.traffic == nil || trafficExcluded(r.URL.Path) {
		return
	}
	ns := g.trafficNamespace(a)
	if isWebSocketUpgrade(r) {
		g.traffic.ObserveStream(ns, status, int64(bytes))
		return
	}
	g.traffic.Observe(ns, status, int64(bytes), dur)
}

// countTraffic adds a request to the request metrics as a count and a status
// only: no size and no latency sample. It is for the routes that keep no record
// of a request's size or duration (routepolicy.LogNone).
func (g *Gateway) countTraffic(r *http.Request, a *trafficAttribution, status int) {
	if g.traffic == nil || trafficExcluded(r.URL.Path) {
		return
	}
	g.traffic.ObserveStream(g.trafficNamespace(a), status, 0)
}

func (g *Gateway) trafficNamespace(a *trafficAttribution) string {
	if a.namespace != "" {
		return a.namespace
	}
	return g.cfg.ClientNamespace
}

// TrafficSnapshot reports what this gateway served over the last minute, or
// nil when it keeps no request metrics.
func (g *Gateway) TrafficSnapshot() *report.TrafficReport {
	if g.traffic == nil {
		return nil
	}
	snap := g.traffic.Snapshot(time.Now())
	return &snap
}
