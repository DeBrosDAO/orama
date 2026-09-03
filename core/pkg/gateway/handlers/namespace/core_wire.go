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

// Tenant restore retry bounds. The disk-backed pass needs nothing but local
// state and runs immediately; the database-backed pass needs a raft leader, so
// it retries with backoff for as long as the process lives.
const (
	tenantRestoreBaseBackoff = 5 * time.Second
	tenantRestoreMaxBackoff  = 2 * time.Minute
)

// restoreTenantClusters brings this node's namespace clusters back up after a
// restart.
//
// It used to sleep five seconds, restore from disk, sleep five more, then try
// the database twelve times and give up. Both sleeps were guesses at how long
// rqlite needs, and the twelve-attempt cap meant a node whose cluster had no
// leader for two minutes left every tenant down until someone restarted the
// gateway by hand. The database pass now retries indefinitely with backoff:
// failing that call IS the readiness signal, so there is nothing to sleep for.
func restoreTenantClusters(ctx context.Context, clusterManager *namespacepkg.ClusterManager, logger *zap.Logger) {
	restored, err := clusterManager.RestoreLocalClustersFromDisk(ctx)
	if err != nil {
		logger.Warn("Disk-based namespace restore failed", zap.Error(err))
	}
	if restored > 0 {
		logger.Info("Restored namespace clusters from local state", zap.Int("count", restored))
	}

	backoff := tenantRestoreBaseBackoff
	for {
		err := clusterManager.RestoreLocalClusters(ctx)
		if err == nil {
			logger.Info("Restored namespace clusters from the cluster database")
			return
		}
		logger.Warn("Namespace cluster restore from the database failed, retrying",
			zap.Duration("retry_in", backoff), zap.Error(err))

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		backoff *= 2
		if backoff > tenantRestoreMaxBackoff {
			backoff = tenantRestoreMaxBackoff
		}
	}
}
