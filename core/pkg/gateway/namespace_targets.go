package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// namespaceGatewayTarget is one live gateway of a namespace: where the main
// gateway proxies that namespace's requests.
type namespaceGatewayTarget struct {
	ip   string
	port int
}

// namespaceGatewayTargetsQuery resolves the live gateway targets for a namespace.
//
// Bugboard #278: this used to require nc.status = 'ready', so a cluster marked
// 'degraded' returned zero rows and the namespace 404'd on EVERY node —
// including nodes whose gateways were perfectly healthy. One node's gateway row
// flipping to 'failed' took a whole tenant offline, which defeats the point of
// running three replicas. A degraded cluster is now served from its healthy
// members: selection is on the per-node status, not the cluster-level rollup, so
// the namespace 404s only when there is genuinely no live gateway.
//
// dn.status = 'active' is the other half of that. A namespace_cluster_nodes row
// says 'running' until something updates it, and nothing did when a NODE went
// away rather than a service — so traffic kept being proxied to a machine the
// fleet had already given up on, until the tenant reconciler pruned it.
const namespaceGatewayTargetsQuery = `
			SELECT COALESCE(dn.internal_ip, dn.ip_address) AS ip, npa.gateway_http_port AS port
			FROM namespace_port_allocations npa
			JOIN namespace_clusters nc ON npa.namespace_cluster_id = nc.id
			JOIN dns_nodes dn ON npa.node_id = dn.id
			JOIN namespace_cluster_nodes ncn
			  ON ncn.namespace_cluster_id = nc.id
			 AND ncn.node_id = npa.node_id
			 AND ncn.role = 'gateway'
			WHERE nc.namespace_name = ?
			  AND nc.status IN ('ready', 'degraded')
			  AND ncn.status = 'running'
			  AND dn.status = 'active'
		`

// namespaceGatewayTargets reads a namespace's live gateways from the cluster
// registry. It is a leader read (the registry handle reads at level=weak), not
// this node's local copy: a node that has not yet applied a freshly provisioned
// namespace's rows read none, and answered 404 "not found" for a namespace the
// leader already served. Only a cache miss pays for it; a hit is served from
// mwCache. No rows is not an error: the namespace has no live gateway.
func (g *Gateway) namespaceGatewayTargets(ctx context.Context, namespace string) ([]namespaceGatewayTarget, error) {
	if g.registry == nil {
		return nil, fmt.Errorf("this gateway has no cluster registry handle to look namespace %q up in", namespace)
	}
	var rows []struct {
		IP   string `db:"ip"`
		Port int    `db:"port"`
	}
	if err := g.registry.Query(client.WithInternalAuth(ctx), &rows, namespaceGatewayTargetsQuery, namespace); err != nil {
		return nil, fmt.Errorf("read the gateways of namespace %q: %w", namespace, err)
	}
	targets := make([]namespaceGatewayTarget, 0, len(rows))
	for _, row := range rows {
		ip := strings.TrimSpace(row.IP)
		if ip == "" || row.Port <= 0 {
			// gateway_http_port is NOT NULL and a node has an address; a row
			// without either is not a target to send a tenant's traffic to.
			continue
		}
		targets = append(targets, namespaceGatewayTarget{ip: ip, port: row.Port})
	}
	return targets, nil
}
