package namespace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	namespacepkg "github.com/DeBrosOfficial/network/pkg/namespace"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"go.uber.org/zap"
)

// WireCoreGateway attaches ClusterManager, spawn/restore, and WebRTC
// reconcilers to the index gateway process. Tenant gateways must not call this.
func WireCoreGateway(ctx context.Context, apiGateway *gateway.Gateway, cfg *gateway.Config, logger *zap.Logger) error {
	if apiGateway == nil || cfg == nil {
		return fmt.Errorf("wire core gateway: nil gateway or config")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	// Both come from cluster_secret_path, which the gateway requires
	// (cmd/gateway loadNodeIdentity). The orama directory used to fall back
	// to $HOME/.orama, which the unit's ProtectHome hides, and a missing
	// secret spawned namespace gateways with no cluster_secret_path — which
	// then had no node identity.
	oramaDir := cfg.DataDir
	if oramaDir == "" {
		return fmt.Errorf("wire core gateway: the config has no orama directory; it is derived from cluster_secret_path")
	}
	if cfg.ClusterSecret == "" {
		return fmt.Errorf("wire core gateway: the config has no cluster secret; spawned namespace gateways need its path")
	}
	ormClient := apiGateway.GetORMClient()
	if ormClient == nil {
		return fmt.Errorf("wire core gateway: no ORM client")
	}

	baseDataDir := filepath.Join(oramaDir, "data", "namespaces")

	var turnEncKey []byte
	turnIKM := cfg.ClusterSecret
	if raw, err := os.ReadFile(filepath.Join(oramaDir, "secrets", secrets.FileName)); err == nil {
		if v := strings.TrimSpace(string(raw)); v != "" {
			turnIKM = v
		}
	}
	if turnIKM != "" {
		if key, keyErr := secrets.DeriveKey(turnIKM, "turn-encryption"); keyErr == nil {
			turnEncKey = key
		}
	}
	clusterSecretPath := filepath.Join(oramaDir, "secrets", "cluster-secret")

	peerID := cfg.NodePeerID

	clusterCfg := namespacepkg.ClusterManagerConfig{
		BaseDomain:            cfg.BaseDomain,
		BaseDataDir:           baseDataDir,
		GlobalRQLiteDSN:       cfg.RQLiteDSN,
		IPFSClusterAPIURL:     cfg.IPFSClusterAPIURL,
		IPFSAPIURL:            cfg.IPFSAPIURL,
		IPFSTimeout:           cfg.IPFSTimeout,
		IPFSReplicationFactor: cfg.IPFSReplicationFactor,
		TurnEncryptionKey:     turnEncKey,
		ClusterSecretPath:     clusterSecretPath,
		SecretsEncryptionKey:  cfg.SecretsEncryptionKey,
		// Bugboard #274: forward the host's ntfy base URL so spawned namespace
		// gateways register an ntfy push provider by default.
		NtfyBaseURL: cfg.NtfyBaseURL,
	}
	clusterManager := namespacepkg.NewClusterManager(ormClient, clusterCfg, logger)
	clusterManager.SetLocalNodeID(peerID)
	apiGateway.SetClusterProvisioner(clusterManager)
	apiGateway.SetNodeRecoverer(clusterManager)
	apiGateway.SetWebRTCManager(clusterManager)

	systemdSpawner := namespacepkg.NewSystemdSpawner(baseDataDir, clusterSecretPath, logger)
	apiGateway.SetSpawnHandler(NewSpawnHandler(systemdSpawner, clusterSecretPath, logger))
	apiGateway.SetNamespaceDeleteHandler(NewDeleteHandler(clusterManager, ormClient, apiGateway.GetIPFSClient(), apiGateway.GetAuditLog(), logger))
	apiGateway.SetNamespaceListHandler(NewListHandler(ormClient, logger))
	apiGateway.SetNamespaceCreateHandler(NewCreateHandler(ormClient, clusterManager, apiGateway.GetAuditLog(), logger))

	logger.Info("Namespace cluster provisioning enabled on index gateway",
		zap.String("base_domain", clusterCfg.BaseDomain),
		zap.String("base_data_dir", baseDataDir))

	clusterManager.StartLeaderLocalityReconciler(ctx)
	clusterManager.StartWebRTCReconciler(ctx)

	// The disk-backed pass runs once, immediately: it needs nothing but local
	// state, and it is what gets this node's tenants up before rqlite has a
	// leader. Everything after that is the reconciler's, which converges on a
	// 60s loop instead of restoring once and stopping.
	go func() {
		restored, err := clusterManager.RestoreLocalClustersFromDisk(ctx)
		if err != nil {
			logger.Warn("Disk-based namespace restore failed", zap.Error(err))
		}
		if restored > 0 {
			logger.Info("Restored namespace clusters from local state", zap.Int("count", restored))
		}
		clusterManager.StartTenantReconciler(ctx)
	}()
	return nil
}
