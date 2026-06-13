package gateway

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// defaultNtfyFanoutTTL bounds how long the active-push-node list is cached
// before re-querying dns_nodes. Matches the DNS heartbeat cadence, so a node
// added/removed is picked up within a heartbeat without hammering rqlite on
// every push.
const defaultNtfyFanoutTTL = 30 * time.Second

// ntfyFanoutResolver resolves the set of ntfy publish base URLs (one per active
// push node) for fan-out delivery, caching the result for a short TTL. Each
// node runs an independent ntfy with no shared store, so a publish must reach
// every node for the subscriber's instance to receive it (bugboard #858).
type ntfyFanoutResolver struct {
	// query returns the public IPs of the currently-active push nodes. Injected
	// so the cache/transform logic is unit-testable without a live cluster.
	query  func(ctx context.Context) ([]string, error)
	scheme string // "https" (prod) / "http" (dev), from the configured base URL
	port   string // explicit port from the base URL, or "" for the scheme default

	ttl      time.Duration
	mu       sync.Mutex
	cached   []string
	cachedAt time.Time
}

// newNtfyFanoutResolver builds a resolver backed by the global dns_nodes table.
func newNtfyFanoutResolver(globalDB client.NetworkClient, scheme, port string, ttl time.Duration) *ntfyFanoutResolver {
	return &ntfyFanoutResolver{
		scheme: scheme,
		port:   port,
		ttl:    ttl,
		query: func(ctx context.Context) ([]string, error) {
			db := globalDB.Database()
			res, err := db.Query(client.WithInternalAuth(ctx), "SELECT ip_address FROM dns_nodes WHERE status = 'active'")
			if err != nil {
				return nil, fmt.Errorf("query active push nodes: %w", err)
			}
			if res == nil {
				return nil, nil
			}
			ips := make([]string, 0, len(res.Rows))
			for _, row := range res.Rows {
				if len(row) == 0 {
					continue
				}
				if ip, ok := row[0].(string); ok && ip != "" {
					ips = append(ips, ip)
				}
			}
			return ips, nil
		},
	}
}

// Hosts returns the cached fan-out base URLs, refreshing from the query when the
// cache is stale. On a query error it returns the last-known list (possibly nil)
// alongside the error, so the caller can decide to fall back to its base URL
// rather than dropping a push.
func (r *ntfyFanoutResolver) Hosts(ctx context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cached != nil && time.Since(r.cachedAt) < r.ttl {
		return r.cached, nil
	}

	ips, err := r.query(ctx)
	if err != nil {
		return r.cached, err
	}

	hosts := make([]string, 0, len(ips))
	suffix := ""
	if r.port != "" {
		suffix = ":" + r.port
	}
	for _, ip := range ips {
		if ip == "" {
			continue
		}
		hosts = append(hosts, r.scheme+"://"+ip+suffix)
	}
	r.cached = hosts
	r.cachedAt = time.Now()
	return hosts, nil
}
