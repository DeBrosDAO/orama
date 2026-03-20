package gateway

import (
	"context"
	"fmt"
	"net"
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
	const numChecks = 7
	ch := make(chan namedResult, numChecks)

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
				// SOCKS5 port not reachable — Anyone relay is not installed/running.
				// Treat as "unavailable" rather than "error" so nodes without Anyone
				// don't report as degraded.
				nr.result = checkResult{Status: "unavailable"}
			}
		}
		ch <- nr
	}()

	// Vault Guardian (TCP connect on WireGuard IP:7500)
	go func() {
		nr := namedResult{name: "vault"}
		start := time.Now()
		vaultAddr := "localhost:7500"
		if g.localWireGuardIP != "" {
			vaultAddr = g.localWireGuardIP + ":7500"
		}
		conn, err := net.DialTimeout("tcp", vaultAddr, 2*time.Second)
		if err != nil {
			nr.result = checkResult{Status: "error", Latency: time.Since(start).String(), Error: fmt.Sprintf("vault-guardian unreachable on port 7500: %v", err)}
		} else {
			conn.Close()
			nr.result = checkResult{Status: "ok", Latency: time.Since(start).String()}
		}
		ch <- nr
	}()

	// WireGuard (check wg0 interface exists and has an IP)
	go func() {
		nr := namedResult{name: "wireguard"}
		iface, err := net.InterfaceByName("wg0")
		if err != nil {
			nr.result = checkResult{Status: "error", Error: "wg0 interface not found"}
		} else if addrs, err := iface.Addrs(); err != nil || len(addrs) == 0 {
			nr.result = checkResult{Status: "error", Error: "wg0 has no addresses"}
		} else {
			nr.result = checkResult{Status: "ok"}
		}
		ch <- nr
	}()

	// Collect
	checks := make(map[string]checkResult, numChecks)
	for i := 0; i < numChecks; i++ {
		nr := <-ch
		checks[nr.name] = nr.result
	}

	overallStatus := aggregateHealthStatus(checks)

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

// aggregateHealthStatus determines the overall health status from individual checks.
// Critical: rqlite or vault down → "unhealthy"
// Non-critical (olric, ipfs, libp2p, anyone, wireguard) error → "degraded"
// "unavailable" means the client was never configured — not an error.
func aggregateHealthStatus(checks map[string]checkResult) string {
	// Critical services — any error means unhealthy
	for _, name := range []string{"rqlite", "vault"} {
		if c := checks[name]; c.Status == "error" {
			return "unhealthy"
		}
	}
	// Non-critical services — any error means degraded
	for name, c := range checks {
		if name == "rqlite" || name == "vault" {
			continue
		}
		if c.Status == "error" {
			return "degraded"
		}
	}
	return "healthy"
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
