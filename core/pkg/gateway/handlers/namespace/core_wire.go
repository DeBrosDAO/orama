package namespace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/encryption"
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
	ormClient := apiGateway.GetORMClient()
	if ormClient == nil {
		return fmt.Errorf("wire core gateway: no ORM client")
	}

	oramaDir := cfg.DataDir
	if oramaDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("wire core gateway: data dir unknown: %w", err)
		}
		oramaDir = filepath.Join(home, ".orama")
	}
	baseDataDir := filepath.Join(oramaDir, "data", "namespaces")

	var turnEncKey []byte
	if cfg.ClusterSecret != "" {
		if key, keyErr := secrets.DeriveKey(cfg.ClusterSecret, "turn-encryption"); keyErr == nil {
			turnEncKey = key
		}
	}
	clusterSecretPath := ""
	if cfg.ClusterSecret != "" {
		clusterSecretPath = filepath.Join(oramaDir, "secrets", "cluster-secret")
	}

	peerID := cfg.NodePeerID
	if peerID == "" {
		if info, err := encryption.LoadIdentity(filepath.Join(oramaDir, "data", "identity.key")); err == nil {
			peerID = info.PeerID.String()
		}
	}

	baseDomain := cfg.BaseDomain
	if baseDomain == "" {
		baseDomain = cfg.DomainName
	}

	clusterCfg := namespacepkg.ClusterManagerConfig{
		BaseDomain:            baseDomain,
		BaseDataDir:           baseDataDir,
		GlobalRQLiteDSN:       cfg.RQLiteDSN,
		IPFSClusterAPIURL:     cfg.IPFSClusterAPIURL,
		IPFSAPIURL:            cfg.IPFSAPIURL,
		IPFSTimeout:           cfg.IPFSTimeout,
		IPFSReplicationFactor: cfg.IPFSReplicationFactor,
		TurnEncryptionKey:     turnEncKey,
		ClusterSecretPath:     clusterSecretPath,
		SecretsEncryptionKey:  cfg.SecretsEncryptionKey,
	}
	clusterManager := namespacepkg.NewClusterManager(ormClient, clusterCfg, logger)
	clusterManager.SetLocalNodeID(peerID)
	apiGateway.SetClusterProvisioner(clusterManager)
	apiGateway.SetNodeRecoverer(clusterManager)
	apiGateway.SetWebRTCManager(clusterManager)

	systemdSpawner := namespacepkg.NewSystemdSpawner(baseDataDir, clusterSecretPath, logger)
	apiGateway.SetSpawnHandler(NewSpawnHandler(systemdSpawner, logger))
	apiGateway.SetNamespaceDeleteHandler(NewDeleteHandler(clusterManager, ormClient, apiGateway.GetIPFSClient(), logger))
	apiGateway.SetNamespaceListHandler(NewListHandler(ormClient, logger))

	logger.Info("Namespace cluster provisioning enabled on index gateway",
		zap.String("base_domain", clusterCfg.BaseDomain),
		zap.String("base_data_dir", baseDataDir))

	clusterManager.StartLeaderLocalityReconciler(ctx)
	clusterManager.StartWebRTCReconciler(ctx)

	go restoreTenantClusters(ctx, clusterManager, logger)
	return nil
}

func restoreTenantClusters(ctx context.Context, clusterManager *namespacepkg.ClusterManager, logger *zap.Logger) {
	time.Sleep(5 * time.Second)
	restored, err := clusterManager.RestoreLocalClustersFromDisk(ctx)
	if err != nil {
		logger.Warn("Disk-based namespace restore failed", zap.Error(err))
	}
	if restored > 0 {
		logger.Info("Restored namespace clusters from local state", zap.Int("count", restored))
	}
	time.Sleep(5 * time.Second)
	for attempt := 1; attempt <= 12; attempt++ {
		if err := clusterManager.RestoreLocalClusters(ctx); err == nil {
			return
		} else {
			logger.Warn("Namespace cluster DB restore failed, retrying",
				zap.Int("attempt", attempt), zap.Error(err))
		}
		time.Sleep(10 * time.Second)
	}
	logger.Error("Failed to restore namespace clusters from DB after all retries")
}
