package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/constants"
	pushntfy "github.com/DeBrosOfficial/network/pkg/push/providers/ntfy"
)

// defaultNtfyFanoutTTL bounds how long the active-push-node list is cached
// before re-querying dns_nodes. Matches the DNS heartbeat cadence, so a node
// added/removed is picked up within a heartbeat without hammering rqlite on
// every push.
const defaultNtfyFanoutTTL = 30 * time.Second

// ntfyFanoutNode is one active push node: its libp2p peer id (the audience of
// the coordination MAC) and its WireGuard overlay address.
type ntfyFanoutNode struct {
	ID         string
	InternalIP string
}

// ntfyFanoutResolver resolves the set of push nodes a publish is fanned out to,
// caching the result for a short TTL. Each node runs an independent ntfy with no
// shared store, so a publish must reach every node for the subscriber's
// instance to receive it (bugboard #858). Targets are the nodes' internal
// gateways on the WireGuard overlay, never their public addresses.
type ntfyFanoutResolver struct {
	// query returns the currently-active push nodes. Injected so the
	// cache/transform logic is unit-testable without a live cluster.
	query func(ctx context.Context) ([]ntfyFanoutNode, error)
	port  int // the nodes' internal gateway port

	ttl      time.Duration
	mu       sync.Mutex
	cached   []pushntfy.FanoutTarget
	cachedAt time.Time
}

// newNtfyFanoutResolver builds a resolver backed by the global dns_nodes table.
func newNtfyFanoutResolver(globalDB client.NetworkClient, ttl time.Duration) *ntfyFanoutResolver {
	return &ntfyFanoutResolver{
		port: constants.GatewayAPIPort,
		ttl:  ttl,
		query: func(ctx context.Context) ([]ntfyFanoutNode, error) {
			// Only nodes with a WireGuard internal IP: the fan-out is a
			// coordination call that must travel the overlay (the receiver
			// refuses any other source), so never fall back to ip_address.
			const q = "SELECT id, internal_ip FROM dns_nodes WHERE status = 'active' AND internal_ip IS NOT NULL AND internal_ip != ''"
			res, err := globalDB.Database().Query(client.WithInternalAuth(ctx), q)
			if err != nil {
				return nil, fmt.Errorf("query active push nodes: %w", err)
			}
			if res == nil {
				return nil, nil
			}
			nodes := make([]ntfyFanoutNode, 0, len(res.Rows))
			for _, row := range res.Rows {
				if len(row) < 2 {
					continue
				}
				id, _ := row[0].(string)
				ip, _ := row[1].(string)
				if id != "" && ip != "" {
					nodes = append(nodes, ntfyFanoutNode{ID: id, InternalIP: ip})
				}
			}
			return nodes, nil
		},
	}
}

// Targets returns the cached fan-out targets, refreshing from the query when the
// cache is stale. A query error is returned as is: the provider fails the send
// rather than publishing somewhere it was not asked to.
func (r *ntfyFanoutResolver) Targets(ctx context.Context) ([]pushntfy.FanoutTarget, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cached != nil && time.Since(r.cachedAt) < r.ttl {
		return r.cached, nil
	}

	nodes, err := r.query(ctx)
	if err != nil {
		return nil, err
	}

	targets := make([]pushntfy.FanoutTarget, 0, len(nodes))
	for _, n := range nodes {
		targets = append(targets, pushntfy.FanoutTarget{
			NodeID:  n.ID,
			BaseURL: "http://" + net.JoinHostPort(n.InternalIP, strconv.Itoa(r.port)),
		})
	}
	r.cached = targets
	r.cachedAt = time.Now()
	return targets, nil
}

// newNtfyFanoutSigner returns the signer that stamps a fan-out request with a
// v2 coordination MAC for the target node. The key is derived per call so a
// gateway without a cluster secret fails each send with that reason instead of
// silently publishing unauthenticated.
func newNtfyFanoutSigner(clusterSecret string, now func() time.Time) func(*http.Request, string) error {
	return func(req *http.Request, nodeID string) error {
		key, err := auth.CoordinationKey(clusterSecret)
		if err != nil {
			return err
		}
		return auth.SignCoordination(key, req, now(), nodeID)
	}
}
