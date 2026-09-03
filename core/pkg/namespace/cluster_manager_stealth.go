package namespace

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
)

// Stealth TURNS-over-443 lifecycle (feat-124, censorship-resistant calling).
//
// Enabling stealth for a namespace whose WebRTC is already running:
//  1. creates DNS A records for the neutral stealth host -> the TURN nodes,
//  2. flips namespace_webrtc_config.stealth_enabled,
//  3. re-spawns the namespace's TURN servers with the stealth domain (the
//     spawner provisions a Let's Encrypt cert for it — hard-fail, never
//     self-signed),
//  4. rewrites cluster-state.json on every node (so DB-less restores keep
//     the stealth domain), and
//  5. restarts the namespace gateways so turn.credentials advertises
//     `turns:<stealth-host>:443` as the final URI-ladder rung.
//
// The SNI router on :443 discovers the route (stealth host -> local TURN TLS
// port) from the TURN config files on disk — no extra registration step.

// stealthDomainFor returns the namespace's stealth TURNS host when stealth is
// enabled in its WebRTC config, else "" (callers treat empty as disabled).
func (cm *ClusterManager) stealthDomainFor(namespaceName string, webrtcCfg *WebRTCConfig) string {
	if webrtcCfg == nil || !webrtcCfg.StealthEnabled {
		return ""
	}
	return turn.StealthHostForNamespace(namespaceName, cm.baseDomain)
}

// EnableWebRTCStealth enables the stealth TURNS:443 path for a namespace.
// Requires WebRTC to already be enabled.
func (cm *ClusterManager) EnableWebRTCStealth(ctx context.Context, namespaceName string) error {
	cluster, webrtcCfg, err := cm.getStealthPrereqs(ctx, namespaceName)
	if err != nil {
		return err
	}
	if webrtcCfg.StealthEnabled {
		return ErrWebRTCStealthAlreadyEnabled
	}

	stealthDomain := turn.StealthHostForNamespace(namespaceName, cm.baseDomain)
	cm.logger.Info("Enabling WebRTC stealth for namespace",
		zap.String("namespace", namespaceName),
		zap.String("stealth_domain", stealthDomain))

	clusterNodes, err := cm.getClusterNodesWithIPs(ctx, cluster.ID)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes: %w", err)
	}
	turnBlocks, err := cm.getWebRTCBlocksByType(ctx, cluster.ID, "turn")
	if err != nil {
		return fmt.Errorf("failed to get TURN allocations for namespace %s: %w", namespaceName, err)
	}
	if len(turnBlocks) == 0 {
		return fmt.Errorf("no TURN allocations found for namespace %s (is WebRTC fully enabled?)", namespaceName)
	}

	// DNS first — cert provisioning and clients both need the name to resolve.
	var turnIPs []string
	for _, block := range turnBlocks {
		for _, n := range clusterNodes {
			if n.NodeID == block.NodeID {
				turnIPs = append(turnIPs, n.PublicIP)
			}
		}
	}
	if err := cm.dnsManager.CreateStealthTURNRecords(ctx, namespaceName, stealthDomain, turnIPs); err != nil {
		return fmt.Errorf("failed to create stealth DNS records: %w", err)
	}

	if err := cm.setStealthEnabled(ctx, cluster.ID, true); err != nil {
		return err
	}

	// Re-spawn TURN with the stealth domain; roll back on failure so the
	// board never claims a stealth endpoint that doesn't terminate TLS.
	if err := cm.respawnTURNWithStealth(ctx, cluster, clusterNodes, turnBlocks, webrtcCfg.TURNSharedSecret, stealthDomain); err != nil {
		cm.rollbackStealthEnable(ctx, cluster.ID, namespaceName)
		return fmt.Errorf("failed to re-spawn TURN with stealth cert (stealth rolled back): %w", err)
	}

	cm.refreshStateAndGateways(ctx, cluster, clusterNodes, stealthDomain, webrtcCfg.TURNSharedSecret)
	cm.logEvent(ctx, cluster.ID, EventWebRTCEnabled, "",
		fmt.Sprintf("WebRTC stealth enabled (%s)", stealthDomain), nil)
	return nil
}

// DisableWebRTCStealth turns the stealth TURNS:443 path off again. TURN and
// the baseline ladder (udp/tcp 3478, turns:5349) keep running.
func (cm *ClusterManager) DisableWebRTCStealth(ctx context.Context, namespaceName string) error {
	cluster, webrtcCfg, err := cm.getStealthPrereqs(ctx, namespaceName)
	if err != nil {
		return err
	}
	if !webrtcCfg.StealthEnabled {
		return ErrWebRTCStealthNotEnabled
	}

	cm.logger.Info("Disabling WebRTC stealth for namespace", zap.String("namespace", namespaceName))

	clusterNodes, err := cm.getClusterNodesWithIPs(ctx, cluster.ID)
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes: %w", err)
	}
	turnBlocks, err := cm.getWebRTCBlocksByType(ctx, cluster.ID, "turn")
	if err != nil {
		return fmt.Errorf("failed to get TURN allocations: %w", err)
	}

	if err := cm.setStealthEnabled(ctx, cluster.ID, false); err != nil {
		return err
	}
	if err := cm.respawnTURNWithStealth(ctx, cluster, clusterNodes, turnBlocks, webrtcCfg.TURNSharedSecret, ""); err != nil {
		return fmt.Errorf("failed to re-spawn TURN without stealth: %w", err)
	}
	if err := cm.dnsManager.DeleteStealthTURNRecords(ctx, namespaceName); err != nil {
		cm.logger.Warn("Failed to delete stealth DNS records", zap.Error(err))
	}
	cm.refreshStateAndGateways(ctx, cluster, clusterNodes, "", webrtcCfg.TURNSharedSecret)
	cm.logEvent(ctx, cluster.ID, EventWebRTCDisabled, "", "WebRTC stealth disabled", nil)
	return nil
}

// getStealthPrereqs validates the cluster exists and WebRTC is enabled,
// returning both records (with the TURN secret already decrypted).
func (cm *ClusterManager) getStealthPrereqs(ctx context.Context, namespaceName string) (*NamespaceCluster, *WebRTCConfig, error) {
	cluster, err := cm.GetClusterByNamespace(ctx, namespaceName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get cluster: %w", err)
	}
	if cluster == nil {
		return nil, nil, ErrClusterNotFound
	}
	webrtcCfg, err := cm.GetWebRTCConfig(ctx, namespaceName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get WebRTC config: %w", err)
	}
	if webrtcCfg == nil {
		return nil, nil, ErrWebRTCNotEnabled
	}
	return cluster, webrtcCfg, nil
}

// setStealthEnabled flips the stealth flag in namespace_webrtc_config.
func (cm *ClusterManager) setStealthEnabled(ctx context.Context, clusterID string, enabled bool) error {
	internalCtx := client.WithInternalAuth(ctx)
	val := 0
	if enabled {
		val = 1
	}
	if _, err := cm.db.Exec(internalCtx,
		`UPDATE namespace_webrtc_config SET stealth_enabled = ? WHERE namespace_cluster_id = ? AND enabled = 1`,
		val, clusterID); err != nil {
		return fmt.Errorf("failed to update stealth_enabled: %w", err)
	}
	return nil
}

// respawnTURNWithStealth stops and re-spawns every TURN instance of the
// cluster with the given stealth domain ("" = stealth off). The spawner
// provisions the stealth cert and writes the new TURN config; the SNI
// router's discovery picks the route change up from disk.
func (cm *ClusterManager) respawnTURNWithStealth(
	ctx context.Context,
	cluster *NamespaceCluster,
	clusterNodes []clusterNodeInfo,
	turnBlocks []WebRTCPortBlock,
	turnSecret, stealthDomain string,
) error {
	// Validate the allocation against live membership before claiming success:
	// a TURN block naming a node that is no longer in the cluster means the
	// allocation is stale, and enabling stealth on it would report a
	// censorship-resistant path that nothing serves.
	for _, block := range turnBlocks {
		found := false
		for i := range clusterNodes {
			if clusterNodes[i].NodeID == block.NodeID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("TURN node %s not found in cluster nodes", block.NodeID)
		}
	}

	// Since bugboard #283 part 2 TURN is host-level: enabling stealth adds this
	// namespace's stealth hostname and cert to its hosts' shared TURN config,
	// which each host applies from its own reconcile.
	//
	// The stop-then-respawn this used to do is now actively wrong — it would tear
	// down a server that is also relaying for OTHER namespaces, dropping their
	// live calls to enable stealth for this one. The shared server picks up a new
	// stealth cert by reloading its tenant set, with no restart at all.
	cm.ReconcileHostTURN(ctx)
	cm.logger.Info("Stealth TURNS enabled; hosts apply it on their next reconcile",
		zap.String("namespace", cluster.NamespaceName),
		zap.String("stealth_domain", stealthDomain))
	return nil
}

// rollbackStealthEnable best-effort reverts the DB flag + DNS records after a
// failed stealth enable, so the system never advertises a half-built path.
func (cm *ClusterManager) rollbackStealthEnable(ctx context.Context, clusterID, namespaceName string) {
	if err := cm.setStealthEnabled(ctx, clusterID, false); err != nil {
		cm.logger.Warn("Stealth rollback: failed to clear stealth_enabled", zap.Error(err))
	}
	if err := cm.dnsManager.DeleteStealthTURNRecords(ctx, namespaceName); err != nil {
		cm.logger.Warn("Stealth rollback: failed to delete DNS records", zap.Error(err))
	}
}

// refreshStateAndGateways rewrites cluster-state.json on all nodes with the
// new stealth domain and restarts the namespace gateways so turn.credentials
// reflects the change. Failures are logged per node (the reconciler converges
// stragglers later via the gatewayConfigInSync drift check).
func (cm *ClusterManager) refreshStateAndGateways(
	ctx context.Context,
	cluster *NamespaceCluster,
	clusterNodes []clusterNodeInfo,
	stealthDomain, turnSecret string,
) {
	turnDomain := fmt.Sprintf("turn.ns-%s.%s", cluster.NamespaceName, cm.baseDomain)

	sfuBlockList, err := cm.getWebRTCBlocksByType(ctx, cluster.ID, "sfu")
	if err != nil {
		cm.logger.Warn("Failed to get SFU allocations for state refresh", zap.Error(err))
	}
	turnBlockList, err := cm.getWebRTCBlocksByType(ctx, cluster.ID, "turn")
	if err != nil {
		cm.logger.Warn("Failed to get TURN allocations for state refresh", zap.Error(err))
	}
	sfuBlocks := make(map[string]*WebRTCPortBlock)
	for i := range sfuBlockList {
		sfuBlocks[sfuBlockList[i].NodeID] = &sfuBlockList[i]
	}
	turnBlocks := make(map[string]*WebRTCPortBlock)
	for i := range turnBlockList {
		turnBlocks[turnBlockList[i].NodeID] = &turnBlockList[i]
	}

	cm.updateClusterStateWithWebRTC(ctx, cluster, clusterNodes, sfuBlocks, turnBlocks, turnDomain, stealthDomain, turnSecret)

	portBlocks, err := cm.portAllocator.GetAllPortBlocks(ctx, cluster.ID)
	if err != nil {
		cm.logger.Warn("Failed to get port blocks for gateway restart after stealth toggle", zap.Error(err))
		return
	}
	nodePortBlocks := make(map[string]*PortBlock)
	for i := range portBlocks {
		nodePortBlocks[portBlocks[i].NodeID] = &portBlocks[i]
	}
	cm.restartGatewaysWithWebRTC(ctx, cluster, clusterNodes, nodePortBlocks, sfuBlocks, turnDomain, stealthDomain, turnSecret)
}
