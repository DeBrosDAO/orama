package ipfs

import (
	"encoding/json"
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
	cfg              *config.Config
	logger           *zap.Logger
	clusterPath      string
	secret           string
	trustedPeersPath string // path to ipfs-cluster-trusted-peers file
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
	trustedPeersPath := ""
	if strings.Contains(dataDir, ".orama") {
		home, err := os.UserHomeDir()
		if err == nil {
			secretsDir := filepath.Join(home, ".orama", "secrets")
			if err := os.MkdirAll(secretsDir, 0700); err == nil {
				secretPath = filepath.Join(secretsDir, "cluster-secret")
				trustedPeersPath = filepath.Join(secretsDir, "ipfs-cluster-trusted-peers")
			}
		}
	}

	// clusterPath tells the loader whether this node has joined before, which
	// decides whether generating a secret is safe or a way to partition it.
	secret, err := loadOrGenerateClusterSecret(secretPath, clusterPath)
	if err != nil {
		return nil, fmt.Errorf("ipfs-cluster secret: %w", err)
	}

	return &ClusterConfigManager{
		cfg:              cfg,
		logger:           logger,
		clusterPath:      clusterPath,
		secret:           secret,
		trustedPeersPath: trustedPeersPath,
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
	cfg.Consensus.CRDT.ClusterName = "orama-cluster"

	// Every authenticated cluster peer is a trusted CRDT writer.
	//
	// This used to be an allowlist built from the secrets/ipfs-cluster-trusted-peers
	// file, and it could never be correct. A joining node receives the CURRENT
	// contents of that file from the node it joins through and appends itself,
	// so it trusts {bootstrap set} ∪ {self} — but nothing ever adds the joiner's
	// ID to the peers that were already running. On a three-node cluster the
	// bootstrap node ends up trusting only itself.
	//
	// In CRDT consensus an untrusted peer's writes are silently dropped by
	// everyone who does not trust it. So a pin or unpin served by any node other
	// than the original bootstrap node applied to that node's local state and
	// was discarded everywhere else — no error, HTTP 200, divergent pinsets.
	// Because tenant traffic is spread across nodes by round-robin DNS, most
	// storage writes and deletes never replicated. Observed on devnet: an unpin
	// served by node 2 left the CID pinned on nodes 1 and 3 indefinitely, which
	// is what made privacy-grade immediate reclaim (bugboard #153) impossible to
	// deliver — the blocks it tries to evict were still pinned elsewhere.
	//
	// Membership is already gated: a peer cannot join the libp2p cluster at all
	// without the shared cluster secret, cluster traffic travels the WireGuard
	// mesh, and node enrolment requires an invite token. The per-peer allowlist
	// added no barrier on top of those — it only introduced an asymmetry that
	// fails open on reads and silently drops writes. Trusting every peer that
	// cleared those gates is the model IPFS-Cluster's "*" is for, and it is what
	// this code already fell back to whenever the file was absent.
	cfg.Consensus.CRDT.TrustedPeers = []string{"*"}

	// The trusted-peers FILE is still maintained (the join handshake exchanges
	// peer IDs through it) — it is simply no longer used to restrict who may
	// write cluster state. Keep recording our own ID so a joining node still
	// receives a complete peer list.
	cm.recordOwnClusterPeerID()

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

// readClusterPeerID reads this node's IPFS Cluster peer ID from identity.json
func (cm *ClusterConfigManager) readClusterPeerID() (string, error) {
	identityPath := filepath.Join(cm.clusterPath, "identity.json")
	data, err := os.ReadFile(identityPath)
	if err != nil {
		return "", fmt.Errorf("failed to read identity.json: %w", err)
	}

	var identity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return "", fmt.Errorf("failed to parse identity.json: %w", err)
	}
	if identity.ID == "" {
		return "", fmt.Errorf("peer ID not found in identity.json")
	}
	return identity.ID, nil
}

// loadTrustedPeers reads trusted peer IDs from the trusted-peers file (one per line)
func (cm *ClusterConfigManager) loadTrustedPeers() []string {
	if cm.trustedPeersPath == "" {
		return nil
	}
	data, err := os.ReadFile(cm.trustedPeersPath)
	if err != nil {
		return nil
	}
	var peers []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			peers = append(peers, line)
		}
	}
	return peers
}

// addTrustedPeer appends a peer ID to the trusted-peers file if not already present
func (cm *ClusterConfigManager) addTrustedPeer(peerID string) error {
	if cm.trustedPeersPath == "" || peerID == "" {
		return nil
	}
	existing := cm.loadTrustedPeers()
	for _, p := range existing {
		if p == peerID {
			return nil // already present
		}
	}
	existing = append(existing, peerID)
	return os.WriteFile(cm.trustedPeersPath, []byte(strings.Join(existing, "\n")+"\n"), 0600)
}

// loadTrustedPeersWithSelf loads trusted peers from file and ensures this node's
// own peer ID is included. Returns nil if no trusted peers file exists.
// recordOwnClusterPeerID persists this node's IPFS Cluster peer ID into the
// shared trusted-peers file, which the join handshake serves to new nodes so
// they learn the cluster's peer set. It no longer influences who may write
// cluster state — see EnsureConfig for why that allowlist was removed.
func (cm *ClusterConfigManager) recordOwnClusterPeerID() {
	ownID, err := cm.readClusterPeerID()
	if err != nil {
		cm.logger.Debug("Could not read own IPFS Cluster peer ID", zap.Error(err))
		return
	}
	if ownID == "" {
		return
	}
	if err := cm.addTrustedPeer(ownID); err != nil {
		cm.logger.Warn("Failed to persist own peer ID to the cluster peers file", zap.Error(err))
	}
}
