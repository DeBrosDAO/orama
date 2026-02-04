package node

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	namespacehandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/namespace"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/namespace"
	"go.uber.org/zap"
)

// startHTTPGateway initializes and starts the full API gateway
// The gateway always runs HTTP on the configured port (default :6001).
// When running with Caddy (nameserver mode), Caddy handles external HTTPS
// and proxies requests to this internal HTTP gateway.
func (n *Node) startHTTPGateway(ctx context.Context) error {
	if !n.config.HTTPGateway.Enabled {
		n.logger.ComponentInfo(logging.ComponentNode, "HTTP Gateway disabled in config")
		return nil
	}

	logFile := filepath.Join(os.ExpandEnv(n.config.Node.DataDir), "..", "logs", "gateway.log")
	logsDir := filepath.Dir(logFile)
	_ = os.MkdirAll(logsDir, 0755)

	gatewayLogger, err := logging.NewFileLogger(logging.ComponentGeneral, logFile, false)
	if err != nil {
		return err
	}

	// DataDir in node config is ~/.orama/data; the orama dir is the parent
	oramaDir := filepath.Join(os.ExpandEnv(n.config.Node.DataDir), "..")

	// Read cluster secret for WireGuard peer exchange auth
	clusterSecret := ""
	if secretBytes, err := os.ReadFile(filepath.Join(oramaDir, "secrets", "cluster-secret")); err == nil {
		clusterSecret = string(secretBytes)
	}

	gwCfg := &gateway.Config{
		ListenAddr:        n.config.HTTPGateway.ListenAddr,
		ClientNamespace:   n.config.HTTPGateway.ClientNamespace,
		BootstrapPeers:    n.config.Discovery.BootstrapPeers,
		NodePeerID:        loadNodePeerIDFromIdentity(n.config.Node.DataDir),
		RQLiteDSN:         n.config.HTTPGateway.RQLiteDSN,
		OlricServers:      n.config.HTTPGateway.OlricServers,
		OlricTimeout:      n.config.HTTPGateway.OlricTimeout,
		IPFSClusterAPIURL: n.config.HTTPGateway.IPFSClusterAPIURL,
		IPFSAPIURL:        n.config.HTTPGateway.IPFSAPIURL,
		IPFSTimeout:       n.config.HTTPGateway.IPFSTimeout,
		BaseDomain:        n.config.HTTPGateway.BaseDomain,
		DataDir:           oramaDir,
		ClusterSecret:     clusterSecret,
	}

	apiGateway, err := gateway.New(gatewayLogger, gwCfg)
	if err != nil {
		return err
	}
	n.apiGateway = apiGateway

	// Wire up ClusterManager for per-namespace cluster provisioning
	if ormClient := apiGateway.GetORMClient(); ormClient != nil {
		baseDataDir := filepath.Join(os.ExpandEnv(n.config.Node.DataDir), "..", "data", "namespaces")
		clusterCfg := namespace.ClusterManagerConfig{
			BaseDomain:      n.config.HTTPGateway.BaseDomain,
			BaseDataDir:     baseDataDir,
			GlobalRQLiteDSN: gwCfg.RQLiteDSN, // Pass global RQLite DSN for namespace gateway auth
		}
		clusterManager := namespace.NewClusterManager(ormClient, clusterCfg, n.logger.Logger)
		clusterManager.SetLocalNodeID(gwCfg.NodePeerID)
		apiGateway.SetClusterProvisioner(clusterManager)

		// Wire spawn handler for distributed namespace instance spawning
		systemdSpawner := namespace.NewSystemdSpawner(baseDataDir, n.logger.Logger)
		spawnHandler := namespacehandlers.NewSpawnHandler(systemdSpawner, n.logger.Logger)
		apiGateway.SetSpawnHandler(spawnHandler)

		// Wire namespace delete handler
		deleteHandler := namespacehandlers.NewDeleteHandler(clusterManager, ormClient, n.logger.Logger)
		apiGateway.SetNamespaceDeleteHandler(deleteHandler)

		n.logger.ComponentInfo(logging.ComponentNode, "Namespace cluster provisioning enabled",
			zap.String("base_domain", clusterCfg.BaseDomain),
			zap.String("base_data_dir", baseDataDir))

		// Restore previously-running namespace cluster processes in background.
		// First try local state files (no DB dependency), then fall back to DB query with retries.
		go func() {
			time.Sleep(5 * time.Second)

			// Try disk-based restore first (instant, no DB needed)
			restored, err := clusterManager.RestoreLocalClustersFromDisk(ctx)
			if err != nil {
				n.logger.ComponentWarn(logging.ComponentNode, "Disk-based namespace restore failed", zap.Error(err))
			}
			if restored > 0 {
				n.logger.ComponentInfo(logging.ComponentNode, "Restored namespace clusters from local state",
					zap.Int("count", restored))
				return
			}

			// No state files found — fall back to DB query with retries
			n.logger.ComponentInfo(logging.ComponentNode, "No local state files, falling back to DB restore")
			time.Sleep(5 * time.Second)
			for attempt := 1; attempt <= 12; attempt++ {
				if err := clusterManager.RestoreLocalClusters(ctx); err == nil {
					return
				} else {
					n.logger.ComponentWarn(logging.ComponentNode, "Namespace cluster restore failed, retrying",
						zap.Int("attempt", attempt), zap.Error(err))
				}
				time.Sleep(10 * time.Second)
			}
			n.logger.ComponentError(logging.ComponentNode, "Failed to restore namespace clusters after all retries")
		}()
	}

	go func() {
		server := &http.Server{
			Addr:    gwCfg.ListenAddr,
			Handler: apiGateway.Routes(),
		}
		n.apiGatewayServer = server

		ln, err := net.Listen("tcp", gwCfg.ListenAddr)
		if err != nil {
			n.logger.ComponentError(logging.ComponentNode, "Failed to bind HTTP gateway",
				zap.String("addr", gwCfg.ListenAddr), zap.Error(err))
			return
		}

		n.logger.ComponentInfo(logging.ComponentNode, "HTTP gateway started",
			zap.String("addr", gwCfg.ListenAddr))
		server.Serve(ln)
	}()

	return nil
}

// startIPFSClusterConfig initializes and ensures IPFS Cluster configuration
func (n *Node) startIPFSClusterConfig() error {
	n.logger.ComponentInfo(logging.ComponentNode, "Initializing IPFS Cluster configuration")

	cm, err := ipfs.NewClusterConfigManager(n.config, n.logger.Logger)
	if err != nil {
		return err
	}
	n.clusterConfigManager = cm

	_ = cm.FixIPFSConfigAddresses()
	if err := cm.EnsureConfig(); err != nil {
		return err
	}

	_ = cm.RepairPeerConfiguration()
	return nil
}
