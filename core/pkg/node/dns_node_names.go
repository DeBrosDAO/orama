package node

import (
	"context"
	"database/sql"
	"errors"

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
	zone := n.config.DNS.NodeNamesZone
	if zone == "" {
		return
	}
	syncer := &nodenames.Syncer{
		Zone:     zone,
		Registry: n.nodeNamesRegistry,
		Chain:    nodenames.ReaderChain{Reader: &chainread.Reader{RPC: gwchainread.ConfigFromEnv().RPCURL}},
	}
	go syncer.Run(ctx, n.reportNodeNamesPass(zone))
	n.logger.ComponentInfo(logging.ComponentNode, "Started node names sync",
		zap.String("zone", zone), zap.Duration("interval", nodenames.SyncInterval))
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
	lastRefused := 0
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
		if len(stats.Refused) != lastRefused {
			for _, r := range stats.Refused {
				n.logger.ComponentWarn(logging.ComponentNode, "Node name not published",
					zap.String("zone", zone), zap.String("reason", r.String()))
			}
		}
		lastRefused = len(stats.Refused)
	}
}
