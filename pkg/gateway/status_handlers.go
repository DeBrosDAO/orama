package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/anyoneproxy"
	"github.com/DeBrosOfficial/network/pkg/client"
)

// Build info (set via -ldflags at build time; defaults for dev)
var (
	BuildVersion = "dev"
	BuildCommit  = ""
	BuildTime    = ""
)

// checkResult holds the result of a single subsystem health check.
type checkResult struct {
	Status  string `json:"status"`            // "ok", "error", "unavailable"
	Latency string `json:"latency,omitempty"` // e.g. "1.2ms"
	Error   string `json:"error,omitempty"`   // set when Status == "error"
	Peers   int    `json:"peers,omitempty"`   // libp2p peer count
}

// cachedHealthResult caches the aggregate health response for 5 seconds.
type cachedHealthResult struct {
	response   any
	httpStatus int
	cachedAt   time.Time
}

const healthCacheTTL = 5 * time.Second

func (g *Gateway) healthHandler(w http.ResponseWriter, r *http.Request) {
	// Serve from cache if fresh
	g.healthCacheMu.RLock()
	cached := g.healthCache
	g.healthCacheMu.RUnlock()
	if cached != nil && time.Since(cached.cachedAt) < healthCacheTTL {
		writeJSON(w, cached.httpStatus, cached.response)
		return
	}

	// Run all checks in parallel with a shared 5s timeout
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	type namedResult struct {
		name   string
		result checkResult
	}
	ch := make(chan namedResult, 5)

	// RQLite
	go func() {
		nr := namedResult{name: "rqlite"}
		if g.sqlDB == nil {
			nr.result = checkResult{Status: "unavailable"}
		} else {
			start := time.Now()
			if err := g.sqlDB.PingContext(ctx); err != nil {
				nr.result = checkResult{Status: "error", Latency: time.Since(start).String(), Error: err.Error()}
			} else {
				nr.result = checkResult{Status: "ok", Latency: time.Since(start).String()}
			}
		}
		ch <- nr
	}()

	// Olric (thread-safe: can be nil or reconnected in background)
	go func() {
		nr := namedResult{name: "olric"}
		g.olricMu.RLock()
		oc := g.olricClient
		g.olricMu.RUnlock()
		if oc == nil {
			nr.result = checkResult{Status: "unavailable"}
		} else {
			start := time.Now()
			if err := oc.Health(ctx); err != nil {
				nr.result = checkResult{Status: "error", Latency: time.Since(start).String(), Error: err.Error()}
			} else {
				nr.result = checkResult{Status: "ok", Latency: time.Since(start).String()}
			}
		}
		ch <- nr
	}()

	// IPFS
	go func() {
		nr := namedResult{name: "ipfs"}
		if g.ipfsClient == nil {
			nr.result = checkResult{Status: "unavailable"}
		} else {
			start := time.Now()
			if err := g.ipfsClient.Health(ctx); err != nil {
				nr.result = checkResult{Status: "error", Latency: time.Since(start).String(), Error: err.Error()}
			} else {
				nr.result = checkResult{Status: "ok", Latency: time.Since(start).String()}
			}
		}
		ch <- nr
	}()

	// LibP2P
	go func() {
		nr := namedResult{name: "libp2p"}
		if g.client == nil {
			nr.result = checkResult{Status: "unavailable"}
		} else if h := g.client.Host(); h == nil {
			nr.result = checkResult{Status: "unavailable"}
		} else {
			peers := len(h.Network().Peers())
			nr.result = checkResult{Status: "ok", Peers: peers}
		}
		ch <- nr
	}()

	// Anyone proxy (SOCKS5)
	go func() {
		nr := namedResult{name: "anyone"}
		if !anyoneproxy.Enabled() {
			nr.result = checkResult{Status: "unavailable"}
		} else {
			start := time.Now()
			if anyoneproxy.Running() {
				nr.result = checkResult{Status: "ok", Latency: time.Since(start).String()}
			} else {
				nr.result = checkResult{Status: "error", Latency: time.Since(start).String(), Error: "SOCKS5 proxy not reachable at " + anyoneproxy.Address()}
			}
		}
		ch <- nr
	}()

	// Collect
	checks := make(map[string]checkResult, 5)
	for i := 0; i < 5; i++ {
		nr := <-ch
		checks[nr.name] = nr.result
	}

	// Aggregate status.
	// Critical: rqlite down → "unhealthy"
	// Non-critical (olric, ipfs, libp2p) error → "degraded"
	// "unavailable" means the client was never configured — not an error.
	overallStatus := "healthy"
	if c := checks["rqlite"]; c.Status == "error" {
		overallStatus = "unhealthy"
	}
	if overallStatus == "healthy" {
		for name, c := range checks {
			if name == "rqlite" {
				continue
			}
			if c.Status == "error" {
				overallStatus = "degraded"
				break
			}
		}
	}

	httpStatus := http.StatusOK
	if overallStatus != "healthy" {
		httpStatus = http.StatusServiceUnavailable
	}

	resp := map[string]any{
		"status": overallStatus,
		"server": map[string]any{
			"started_at": g.startedAt,
			"uptime":     time.Since(g.startedAt).String(),
		},
		"checks": checks,
	}

	// Include namespace health if available (populated by namespace health loop)
	if nsHealth := g.getNamespaceHealth(); nsHealth != nil {
		resp["namespaces"] = nsHealth
	}

	// Cache
	g.healthCacheMu.Lock()
	g.healthCache = &cachedHealthResult{
		response:   resp,
		httpStatus: httpStatus,
		cachedAt:   time.Now(),
	}
	g.healthCacheMu.Unlock()

	writeJSON(w, httpStatus, resp)
}

// pingHandler is a lightweight internal endpoint used for peer-to-peer
// health probing over the WireGuard mesh. No subsystem checks — just
// confirms the gateway process is alive and returns its node ID.
func (g *Gateway) pingHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id": g.nodePeerID,
		"status":  "ok",
	})
}

// statusHandler aggregates server uptime and network status
func (g *Gateway) statusHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	// Use internal auth context to bypass client credential requirements
	ctx := client.WithInternalAuth(r.Context())
	status, err := g.client.Network().GetStatus(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"server": map[string]any{
			"started_at": g.startedAt,
			"uptime":     time.Since(g.startedAt).String(),
		},
		"network": status,
	})
}

// versionHandler returns gateway build/runtime information
func (g *Gateway) versionHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    BuildVersion,
		"commit":     BuildCommit,
		"build_time": BuildTime,
		"started_at": g.startedAt,
		"uptime":     time.Since(g.startedAt).String(),
	})
}

// tlsCheckHandler validates if a domain should receive a TLS certificate
// Used by Caddy's on-demand TLS feature to prevent abuse
func (g *Gateway) tlsCheckHandler(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		http.Error(w, "domain parameter required", http.StatusBadRequest)
		return
	}

	// Get base domain from config
	baseDomain := "dbrs.space"
	if g.cfg != nil && g.cfg.BaseDomain != "" {
		baseDomain = g.cfg.BaseDomain
	}

	// Allow any subdomain of our base domain
	if strings.HasSuffix(domain, "."+baseDomain) || domain == baseDomain {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Domain not allowed - only allow subdomains of our base domain
	// Custom domains would need to be verified separately
	http.Error(w, "domain not allowed", http.StatusForbidden)
}
