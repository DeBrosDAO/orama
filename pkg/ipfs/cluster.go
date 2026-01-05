package ipfs

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"go.uber.org/zap"
)

// ClusterConfigManager manages IPFS Cluster configuration files
type ClusterConfigManager struct {
	cfg         *config.Config
	logger      *zap.Logger
	clusterPath string
	secret      string
}

// NewClusterConfigManager creates a new IPFS Cluster config manager
func NewClusterConfigManager(cfg *config.Config, logger *zap.Logger) (*ClusterConfigManager, error) {
	dataDir := cfg.Node.DataDir
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}

	clusterPath := filepath.Join(dataDir, "ipfs-cluster")
	nodeNames := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}
	for _, nodeName := range nodeNames {
		if strings.Contains(dataDir, nodeName) {
			if filepath.Base(filepath.Dir(dataDir)) == nodeName || filepath.Base(dataDir) == nodeName {
				clusterPath = filepath.Join(dataDir, "ipfs-cluster")
			} else {
				clusterPath = filepath.Join(dataDir, nodeName, "ipfs-cluster")
			}
			break
		}
	}

	secretPath := filepath.Join(dataDir, "..", "cluster-secret")
	if strings.Contains(dataDir, ".orama") {
		home, err := os.UserHomeDir()
		if err == nil {
			secretsDir := filepath.Join(home, ".orama", "secrets")
			if err := os.MkdirAll(secretsDir, 0700); err == nil {
				secretPath = filepath.Join(secretsDir, "cluster-secret")
			}
		}
	}

	secret, err := loadOrGenerateClusterSecret(secretPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load/generate cluster secret: %w", err)
	}

	return &ClusterConfigManager{
		cfg:         cfg,
		logger:      logger,
		clusterPath: clusterPath,
		secret:      secret,
	}, nil
}

// EnsureConfig ensures the IPFS Cluster service.json exists and is properly configured
func (cm *ClusterConfigManager) EnsureConfig() error {
	if cm.cfg.Database.IPFS.ClusterAPIURL == "" {
		return nil
	}

	serviceJSONPath := filepath.Join(cm.clusterPath, "service.json")
	clusterPort, restAPIPort, err := parseClusterPorts(cm.cfg.Database.IPFS.ClusterAPIURL)
	if err != nil {
		return err
	}

	ipfsPort, err := parseIPFSPort(cm.cfg.Database.IPFS.APIURL)
	if err != nil {
		return err
	}

	nodeName := "node-1"
	possibleNames := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}
	for _, name := range possibleNames {
		if strings.Contains(cm.cfg.Node.DataDir, name) || strings.Contains(cm.cfg.Node.ID, name) {
			nodeName = name
			break
		}
	}

	proxyPort := clusterPort + 1
	pinSvcPort := clusterPort + 3
	clusterListenPort := clusterPort + 4

	if _, err := os.Stat(serviceJSONPath); os.IsNotExist(err) {
		initCmd := exec.Command("ipfs-cluster-service", "init", "--force")
		initCmd.Env = append(os.Environ(), "IPFS_CLUSTER_PATH="+cm.clusterPath)
		_ = initCmd.Run()
	}

	cfg, err := cm.loadOrCreateConfig(serviceJSONPath)
	if err != nil {
		return err
	}

	cfg.Cluster.Peername = nodeName
	cfg.Cluster.Secret = cm.secret
	cfg.Cluster.ListenMultiaddress = []string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", clusterListenPort)}
	cfg.Consensus.CRDT.ClusterName = "debros-cluster"
	cfg.Consensus.CRDT.TrustedPeers = []string{"*"}
	cfg.API.RestAPI.HTTPListenMultiaddress = fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", restAPIPort)
	cfg.API.IPFSProxy.ListenMultiaddress = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", proxyPort)
	cfg.API.IPFSProxy.NodeMultiaddress = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)
	cfg.API.PinSvcAPI.HTTPListenMultiaddress = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", pinSvcPort)
	cfg.IPFSConnector.IPFSHTTP.NodeMultiaddress = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)

	return cm.saveConfig(serviceJSONPath, cfg)
}

// FixIPFSConfigAddresses fixes localhost addresses in IPFS config
func (cm *ClusterConfigManager) FixIPFSConfigAddresses() error {
	if cm.cfg.Database.IPFS.APIURL == "" {
		return nil
	}

	dataDir := cm.cfg.Node.DataDir
	if strings.HasPrefix(dataDir, "~") {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, dataDir[1:])
	}

	possiblePaths := []string{
		filepath.Join(dataDir, "ipfs", "repo"),
		filepath.Join(dataDir, "node-1", "ipfs", "repo"),
		filepath.Join(dataDir, "node-2", "ipfs", "repo"),
		filepath.Join(filepath.Dir(dataDir), "node-1", "ipfs", "repo"),
		filepath.Join(filepath.Dir(dataDir), "node-2", "ipfs", "repo"),
	}

	var ipfsRepoPath string
	for _, path := range possiblePaths {
		if _, err := os.Stat(filepath.Join(path, "config")); err == nil {
			ipfsRepoPath = path
			break
		}
	}

	if ipfsRepoPath == "" {
		return nil
	}

	ipfsPort, _ := parseIPFSPort(cm.cfg.Database.IPFS.APIURL)
	gatewayPort := 8080
	if strings.Contains(dataDir, "node2") || ipfsPort == 5002 {
		gatewayPort = 8081
	} else if strings.Contains(dataDir, "node3") || ipfsPort == 5003 {
		gatewayPort = 8082
	}

	correctAPIAddr := fmt.Sprintf(`["/ip4/0.0.0.0/tcp/%d"]`, ipfsPort)
	fixCmd := exec.Command("ipfs", "config", "--json", "Addresses.API", correctAPIAddr)
	fixCmd.Env = append(os.Environ(), "IPFS_PATH="+ipfsRepoPath)
	_ = fixCmd.Run()

	correctGatewayAddr := fmt.Sprintf(`["/ip4/0.0.0.0/tcp/%d"]`, gatewayPort)
	fixCmd = exec.Command("ipfs", "config", "--json", "Addresses.Gateway", correctGatewayAddr)
	fixCmd.Env = append(os.Environ(), "IPFS_PATH="+ipfsRepoPath)
	_ = fixCmd.Run()

	return nil
}

func (cm *ClusterConfigManager) isIPFSRunning(port int) bool {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v0/id", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

func (cm *ClusterConfigManager) createTemplateConfig() *ClusterServiceConfig {
	cfg := &ClusterServiceConfig{}
	cfg.Cluster.LeaveOnShutdown = false
	cfg.Cluster.PeerAddresses = []string{}
	cfg.Consensus.CRDT.TrustedPeers = []string{"*"}
	cfg.Consensus.CRDT.Batching.MaxBatchSize = 0
	cfg.Consensus.CRDT.Batching.MaxBatchAge = "0s"
	cfg.Consensus.CRDT.RepairInterval = "1h0m0s"
	cfg.Raw = make(map[string]interface{})
	return cfg
}
