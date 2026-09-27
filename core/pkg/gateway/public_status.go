package gateway

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/DeBrosOfficial/network/pkg/gateway/statuspage"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

const (
	// publicStatusTTL is how long one public status body is served. The
	// endpoint is open to the internet, so every request past the first in a
	// window is answered from memory.
	publicStatusTTL = 5 * time.Second
	// uptimeHistoryTTL is how long the uptime history is reused: it gains a
	// sample a minute.
	uptimeHistoryTTL = time.Minute
)

// publicStatusBody is /v1/status: the fields it always had, and the public
// view of the network when this gateway assembles one.
type publicStatusBody struct {
	Status string         `json:"status"`
	Server map[string]any `json:"server"`
	*cluster.PublicStatus
}

// publicStatusCache holds the last public view and uptime history. Its lock
// guards the fields only: nothing holds it across network or database I/O.
type publicStatusCache struct {
	mu        sync.Mutex
	status    *cluster.PublicStatus
	statusAt  time.Time
	history   cluster.UptimeHistory
	historyAt time.Time
	build     singleflight.Group
}

// statusHandler serves the unauthenticated /status and /v1/status. It shows
// what anyone may see — each service's state and uptime, the chain's
// progress, the network's load — and nothing that names a node (see
// cluster.PublicStatus). It used to embed the network status (peer ids,
// swarm addresses), which is at /v1/network/status now, for operators and
// nodes (networkStatusHandler). A browser asking for /status gets the page.
func (g *Gateway) statusHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/status" {
		// /status answers a browser with the page and anything else with
		// JSON, so a shared cache must key on Accept.
		w.Header().Add("Vary", "Accept")
	}
	if r.URL.Path == "/status" && strings.Contains(r.Header.Get("Accept"), "text/html") {
		statuspage.ServePage(w, r)
		return
	}
	body := publicStatusBody{Status: "ok", Server: g.serverInfo()}
	if g.telemetry != nil {
		body.PublicStatus = g.publicStatus(r.Context())
	}
	w.Header().Set("Cache-Control", "public, max-age=5")
	writeJSON(w, http.StatusOK, body)
}

// publicBuildTimeout bounds one build of the public view, independent of the
// request that started it: a caller that goes away must not cancel the build
// other callers are waiting on.
const publicBuildTimeout = 20 * time.Second

// publicStatus is the cached public view. Concurrent requests share one
// build, and a failed build is cached for the same TTL as a good one, so a
// slow registry costs one attempt per TTL rather than one per request. When
// no snapshot can be assembled the view says the state is unknown rather
// than repeating an old answer.
func (g *Gateway) publicStatus(ctx context.Context) *cluster.PublicStatus {
	c := &g.publicCache
	if ps := c.fresh(time.Now()); ps != nil {
		return ps
	}
	ch := c.build.DoChan("public", func() (any, error) {
		bctx, cancel := context.WithTimeout(context.Background(), publicBuildTimeout)
		defer cancel()
		ps := g.buildPublicStatus(bctx)
		c.store(ps, time.Now())
		return ps, nil
	})
	select {
	case <-ctx.Done():
		return unknownPublicStatus(time.Now())
	case res := <-ch:
		return res.Val.(*cluster.PublicStatus)
	}
}

func (g *Gateway) buildPublicStatus(ctx context.Context) *cluster.PublicStatus {
	now := time.Now()
	snap, err := g.telemetry.agg.Snapshot(ctx)
	if err != nil {
		g.logger.Logger.Warn("public status: cluster snapshot unavailable", zap.Error(err))
		return unknownPublicStatus(now)
	}
	ps := cluster.Public(snap, g.uptimeHistory(ctx, now))
	return &ps
}

func unknownPublicStatus(now time.Time) *cluster.PublicStatus {
	return &cluster.PublicStatus{Overall: cluster.StateUnknown, Headline: "Status unavailable", UpdatedAt: now.UTC()}
}

func (c *publicStatusCache) fresh(now time.Time) *cluster.PublicStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status != nil && now.Sub(c.statusAt) < publicStatusTTL {
		return c.status
	}
	return nil
}

func (c *publicStatusCache) store(ps *cluster.PublicStatus, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status, c.statusAt = ps, now
}

// uptimeHistory is the uptime history, reused for uptimeHistoryTTL. A read
// failure leaves the history empty for this build and is logged: the page then
// shows no uptime rather than an invented one.
func (g *Gateway) uptimeHistory(ctx context.Context, now time.Time) cluster.UptimeHistory {
	c := &g.publicCache
	c.mu.Lock()
	cached, at := c.history, c.historyAt
	c.mu.Unlock()
	if cached != nil && now.Sub(at) < uptimeHistoryTTL {
		return cached
	}
	h, err := g.telemetry.uptime.History(ctx, now)
	if err != nil {
		g.logger.Logger.Warn("public status: uptime history unavailable", zap.Error(err))
		return cluster.UptimeHistory{}
	}
	c.mu.Lock()
	c.history, c.historyAt = h, now
	c.mu.Unlock()
	return h
}
