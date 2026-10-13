package node

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/chainread"
	gwchainread "github.com/DeBrosOfficial/network/pkg/gateway/handlers/chainread"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/nodenames"
)

// startNodeNamesSync publishes the chain's node identification names in this cluster's DNS when
// dns.node_names_zone is set. The cluster whose nameservers answer the zone sets it; every other
// node leaves it empty and this does nothing. The chain is read through the co-located node's
// RPC, the one the gateway's /v1/chain/ proxy reads, and the rows are written into the registry's
// dns_records under their own tag (nodenames.RecordNamespace). Every node of the cluster that sets
// the zone runs the same idempotent sync, so the zone survives the loss of any one of them.
func (n *Node) startNodeNamesSync(ctx context.Context) {
	syncer := n.nodeNamesSyncer()
	if syncer == nil {
		return
	}
	go syncer.Run(ctx, n.reportNodeNamesPass(syncer.Zone))
	n.logger.ComponentInfo(logging.ComponentNode, "Started node names sync",
		zap.String("zone", syncer.Zone), zap.Duration("interval", nodenames.SyncInterval))
}

// nodeNamesSyncer is the sync this node runs, or nil when dns.node_names_zone is empty.
func (n *Node) nodeNamesSyncer() *nodenames.Syncer {
	zone := n.config.DNS.NodeNamesZone
	if zone == "" {
		return nil
	}
	return &nodenames.Syncer{
		Zone:     zone,
		Registry: n.nodeNamesRegistry,
		Chain:    nodenames.ReaderChain{Reader: &chainread.Reader{RPC: gwchainread.ConfigFromEnv().RPCURL}},
	}
}

// nodeNamesRegistry is the cluster registry's database handle, or why there is none yet.
func (n *Node) nodeNamesRegistry() (*sql.DB, error) {
	adapter := n.getRQLiteAdapter()
	if adapter == nil {
		return nil, errors.New("the rqlite adapter is not initialized")
	}
	return adapter.GetSQLDB(), nil
}

// reportNodeNamesPass logs what a pass did: a failure every time, a change when it happens, and the
// names it refused whenever that set changes, so a bad claim is seen once and not every minute.
func (n *Node) reportNodeNamesPass(zone string) func(nodenames.Stats, error) {
	lastRefused := ""
	return func(stats nodenames.Stats, err error) {
		if err != nil {
			n.logger.ComponentWarn(logging.ComponentNode, "Node names sync failed",
				zap.String("zone", zone), zap.Error(err))
			return
		}
		if stats.Changed() {
			n.logger.ComponentInfo(logging.ComponentNode, "Node names synced",
				zap.String("zone", zone), zap.Int("names", stats.Names),
				zap.Int("added", stats.Added), zap.Int("reactivated", stats.Reactivated),
				zap.Int("removed", stats.Removed), zap.Int("remaining", stats.Remaining))
		}
		if refused := refusedKey(stats.Refused); refused != lastRefused {
			for _, r := range stats.Refused {
				n.logger.ComponentWarn(logging.ComponentNode, "Node name not published",
					zap.String("zone", zone), zap.String("reason", r.String()))
			}
			lastRefused = refused
		}
		if stats.RemovalsHeld > 0 {
			n.logger.ComponentWarn(logging.ComponentNode, "Node name removals held back",
				zap.String("zone", zone), zap.Int("held", stats.RemovalsHeld), zap.Bool("chain_catching_up", stats.CatchingUp))
		}
	}
}

// refusedKey identifies a set of refusals, so a different set of the same size is logged too.
func refusedKey(refused []nodenames.Refusal) string {
	keys := make([]string, len(refused))
	for i, r := range refused {
		keys[i] = r.String()
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n")
}
