package node

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	namespacehandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/namespace"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/namespace"
	"github.com/DeBrosOfficial/network/pkg/secrets"
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

	// Read API key HMAC secret for hashing API keys before storage
	apiKeyHMACSecret := ""
	if secretBytes, err := os.ReadFile(filepath.Join(oramaDir, "secrets", "api-key-hmac-secret")); err == nil {
		apiKeyHMACSecret = strings.TrimSpace(string(secretBytes))
	}

	// Read RQLite credentials for authenticated DB connections
	rqlitePassword := ""
	if secretBytes, err := os.ReadFile(filepath.Join(oramaDir, "secrets", "rqlite-password")); err == nil {
		rqlitePassword = strings.TrimSpace(string(secretBytes))
	}

	// Read the serverless secrets encryption key (bugboard #837). Must be the
	// SAME value on every namespace-gateway node so a secret encrypted by one
	// process decrypts on another; an empty value makes get_secret fail loudly
	// (the manager refuses an ephemeral key in production).
	secretsEncryptionKey := ""
	if secretBytes, err := os.ReadFile(filepath.Join(oramaDir, "secrets", "secrets-encryption-key")); err == nil {
		secretsEncryptionKey = strings.TrimSpace(string(secretBytes))
	}

	gwCfg := &gateway.Config{
		ListenAddr:           n.config.HTTPGateway.ListenAddr,
		ClientNamespace:      n.config.HTTPGateway.ClientNamespace,
		BootstrapPeers:       n.config.Discovery.BootstrapPeers,
		NodePeerID:           loadNodePeerIDFromIdentity(n.config.Node.DataDir),
		RQLiteDSN:            n.config.HTTPGateway.RQLiteDSN,
		OlricServers:         n.config.HTTPGateway.OlricServers,
		OlricTimeout:         n.config.HTTPGateway.OlricTimeout,
		IPFSClusterAPIURL:    n.config.HTTPGateway.IPFSClusterAPIURL,
		IPFSAPIURL:           n.config.HTTPGateway.IPFSAPIURL,
		IPFSTimeout:          n.config.HTTPGateway.IPFSTimeout,
		BaseDomain:           n.config.HTTPGateway.BaseDomain,
		DataDir:              oramaDir,
		RQLiteUsername:       "orama",
		RQLitePassword:       rqlitePassword,
		ClusterSecret:        clusterSecret,
		APIKeyHMACSecret:     apiKeyHMACSecret,
		SecretsEncryptionKey: secretsEncryptionKey,
		WebRTCEnabled:        n.config.HTTPGateway.WebRTC.Enabled,
		SFUPort:              n.config.HTTPGateway.WebRTC.SFUPort,
		TURNDomain:           n.config.HTTPGateway.WebRTC.TURNDomain,
		TURNSecret:           n.config.HTTPGateway.WebRTC.TURNSecret,
	}

	apiGateway, err := gateway.New(gatewayLogger, gwCfg)
	if err != nil {
		return err
	}
	n.apiGateway = apiGateway

	// Wire up ClusterManager for per-namespace cluster provisioning
	if ormClient := apiGateway.GetORMClient(); ormClient != nil {
		baseDataDir := filepath.Join(os.ExpandEnv(n.config.Node.DataDir), "..", "data", "namespaces")
		// Derive TURN encryption key from cluster secret (nil if no secret available)
		var turnEncKey []byte
		if clusterSecret != "" {
			if key, keyErr := secrets.DeriveKey(clusterSecret, "turn-encryption"); keyErr == nil {
				turnEncKey = key
			}
		}

		// Bug #215 fix: tell the namespace cluster manager where the
		// host's cluster-secret file lives. Spawned namespace gateways
		// read it on startup to derive the cluster-wide Ed25519 JWT
		// signing key. The path resolves to the same file the host
		// gateway reads above (line ~45) — keeps the secret on disk
		// once, just referenced from the namespace gateway YAML.
		clusterSecretPath := ""
		if clusterSecret != "" {
			clusterSecretPath = filepath.Join(oramaDir, "secrets", "cluster-secret")
		}

		clusterCfg := namespace.ClusterManagerConfig{
			BaseDomain:            n.config.HTTPGateway.BaseDomain,
			BaseDataDir:           baseDataDir,
			GlobalRQLiteDSN:       gwCfg.RQLiteDSN, // Pass global RQLite DSN for namespace gateway auth
			IPFSClusterAPIURL:     gwCfg.IPFSClusterAPIURL,
			IPFSAPIURL:            gwCfg.IPFSAPIURL,
			IPFSTimeout:           gwCfg.IPFSTimeout,
			IPFSReplicationFactor: n.config.Database.IPFS.ReplicationFactor,
			TurnEncryptionKey:     turnEncKey,
			ClusterSecretPath:     clusterSecretPath,
			// Bugboard #837 follow-up: forward the host's serverless secrets
			// encryption key (read once above) so spawned namespace gateways
			// can manage function secrets. Reuses the same variable the host
			// gateway uses — no second file read.
			SecretsEncryptionKey: secretsEncryptionKey,
		}
		clusterManager := namespace.NewClusterManager(ormClient, clusterCfg, n.logger.Logger)
		clusterManager.SetLocalNodeID(gwCfg.NodePeerID)
		apiGateway.SetClusterProvisioner(clusterManager)
		apiGateway.SetNodeRecoverer(clusterManager)
		apiGateway.SetWebRTCManager(clusterManager)

		// Wire spawn handler for distributed namespace instance spawning.
		// Forwards the host's cluster_secret_path through to spawned
		// namespace gateways (bug #215 fix; same rationale as the
		// ClusterManager spawner above).
		systemdSpawner := namespace.NewSystemdSpawner(baseDataDir, clusterSecretPath, n.logger.Logger)
		spawnHandler := namespacehandlers.NewSpawnHandler(systemdSpawner, n.logger.Logger)
		apiGateway.SetSpawnHandler(spawnHandler)

		// Wire namespace delete handler (with IPFS client for content unpinning)
		deleteHandler := namespacehandlers.NewDeleteHandler(clusterManager, ormClient, apiGateway.GetIPFSClient(), n.logger.Logger)
		apiGateway.SetNamespaceDeleteHandler(deleteHandler)

		// Wire namespace list handler
		nsListHandler := namespacehandlers.NewListHandler(ormClient, n.logger.Logger)
		apiGateway.SetNamespaceListHandler(nsListHandler)

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
			Addr:              gwCfg.ListenAddr,
			Handler:           apiGateway.Routes(),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      120 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20, // 1MB
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
