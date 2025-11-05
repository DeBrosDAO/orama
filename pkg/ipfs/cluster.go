package ipfs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/config"
)

// ClusterConfigManager manages IPFS Cluster configuration files
type ClusterConfigManager struct {
	cfg         *config.Config
	logger      *zap.Logger
	clusterPath string
	secret      string
}

// ClusterServiceConfig represents the structure of service.json
type ClusterServiceConfig struct {
	Cluster struct {
		Peername           string   `json:"peername"`
		Secret             string   `json:"secret"`
		LeaveOnShutdown    bool     `json:"leave_on_shutdown"`
		ListenMultiaddress []string `json:"listen_multiaddress"`
		PeerAddresses      []string `json:"peer_addresses"`
		// ... other fields kept from template
	} `json:"cluster"`
	Consensus struct {
		CRDT struct {
			ClusterName  string   `json:"cluster_name"`
			TrustedPeers []string `json:"trusted_peers"`
			Batching     struct {
				MaxBatchSize int    `json:"max_batch_size"`
				MaxBatchAge  string `json:"max_batch_age"`
			} `json:"batching"`
			RepairInterval string `json:"repair_interval"`
		} `json:"crdt"`
	} `json:"consensus"`
	API struct {
		IPFSProxy struct {
			ListenMultiaddress string `json:"listen_multiaddress"`
			NodeMultiaddress   string `json:"node_multiaddress"`
		} `json:"ipfsproxy"`
		PinSvcAPI struct {
			HTTPListenMultiaddress string `json:"http_listen_multiaddress"`
		} `json:"pinsvcapi"`
		RestAPI struct {
			HTTPListenMultiaddress string `json:"http_listen_multiaddress"`
		} `json:"restapi"`
	} `json:"api"`
	IPFSConnector struct {
		IPFSHTTP struct {
			NodeMultiaddress string `json:"node_multiaddress"`
		} `json:"ipfshttp"`
	} `json:"ipfs_connector"`
	// Keep rest of fields as raw JSON to preserve structure
	Raw map[string]interface{} `json:"-"`
}

// NewClusterConfigManager creates a new IPFS Cluster config manager
func NewClusterConfigManager(cfg *config.Config, logger *zap.Logger) (*ClusterConfigManager, error) {
	// Expand data directory path
	dataDir := cfg.Node.DataDir
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}

	// Determine cluster path based on data directory structure
	// Check if dataDir contains specific node names (e.g., ~/.debros/bootstrap, ~/.debros/node2)
	clusterPath := filepath.Join(dataDir, "ipfs-cluster")
	if strings.Contains(dataDir, "bootstrap") {
		// Check if bootstrap is a direct child
		if filepath.Base(filepath.Dir(dataDir)) == "bootstrap" || filepath.Base(dataDir) == "bootstrap" {
			clusterPath = filepath.Join(dataDir, "ipfs-cluster")
		} else {
			clusterPath = filepath.Join(dataDir, "bootstrap", "ipfs-cluster")
		}
	} else if strings.Contains(dataDir, "node2") {
		if filepath.Base(filepath.Dir(dataDir)) == "node2" || filepath.Base(dataDir) == "node2" {
			clusterPath = filepath.Join(dataDir, "ipfs-cluster")
		} else {
			clusterPath = filepath.Join(dataDir, "node2", "ipfs-cluster")
		}
	} else if strings.Contains(dataDir, "node3") {
		if filepath.Base(filepath.Dir(dataDir)) == "node3" || filepath.Base(dataDir) == "node3" {
			clusterPath = filepath.Join(dataDir, "ipfs-cluster")
		} else {
			clusterPath = filepath.Join(dataDir, "node3", "ipfs-cluster")
		}
	}

	// Load or generate cluster secret
	secretPath := filepath.Join(dataDir, "..", "cluster-secret")
	if strings.Contains(dataDir, ".debros") {
		// Try to find cluster-secret in ~/.debros
		home, err := os.UserHomeDir()
		if err == nil {
			secretPath = filepath.Join(home, ".debros", "cluster-secret")
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
		cm.logger.Debug("IPFS Cluster API URL not configured, skipping cluster config")
		return nil
	}

	serviceJSONPath := filepath.Join(cm.clusterPath, "service.json")

	// Parse ports from URLs
	clusterPort, restAPIPort, err := parseClusterPorts(cm.cfg.Database.IPFS.ClusterAPIURL)
	if err != nil {
		return fmt.Errorf("failed to parse cluster API URL: %w", err)
	}

	ipfsPort, err := parseIPFSPort(cm.cfg.Database.IPFS.APIURL)
	if err != nil {
		return fmt.Errorf("failed to parse IPFS API URL: %w", err)
	}

	// Determine node name
	nodeName := cm.cfg.Node.Type
	if nodeName == "node" {
		// Try to extract from data dir or ID
		if strings.Contains(cm.cfg.Node.DataDir, "node2") || strings.Contains(cm.cfg.Node.ID, "node2") {
			nodeName = "node2"
		} else if strings.Contains(cm.cfg.Node.DataDir, "node3") || strings.Contains(cm.cfg.Node.ID, "node3") {
			nodeName = "node3"
		} else {
			nodeName = "node"
		}
	}

	// Calculate ports based on pattern
	proxyPort := clusterPort - 1
	pinSvcPort := clusterPort + 1
	clusterListenPort := clusterPort + 2

	// If config doesn't exist, initialize it with ipfs-cluster-service init
	// This ensures we have all required sections (datastore, informer, etc.)
	if _, err := os.Stat(serviceJSONPath); os.IsNotExist(err) {
		cm.logger.Info("Initializing cluster config with ipfs-cluster-service init")
		initCmd := exec.Command("ipfs-cluster-service", "init", "--force")
		initCmd.Env = append(os.Environ(), "IPFS_CLUSTER_PATH="+cm.clusterPath)
		if err := initCmd.Run(); err != nil {
			cm.logger.Warn("Failed to initialize cluster config with ipfs-cluster-service init, will create minimal template", zap.Error(err))
		}
	}

	// Load existing config or create new
	cfg, err := cm.loadOrCreateConfig(serviceJSONPath)
	if err != nil {
		return fmt.Errorf("failed to load/create config: %w", err)
	}

	// Update configuration
	cfg.Cluster.Peername = nodeName
	cfg.Cluster.Secret = cm.secret
	cfg.Cluster.ListenMultiaddress = []string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", clusterListenPort)}
	cfg.Consensus.CRDT.ClusterName = "debros-cluster"
	cfg.Consensus.CRDT.TrustedPeers = []string{"*"}

	// API endpoints
	cfg.API.RestAPI.HTTPListenMultiaddress = fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", restAPIPort)
	cfg.API.IPFSProxy.ListenMultiaddress = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", proxyPort)
	cfg.API.IPFSProxy.NodeMultiaddress = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort) // FIX: Correct path!
	cfg.API.PinSvcAPI.HTTPListenMultiaddress = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", pinSvcPort)

	// IPFS connector (also needs to be set)
	cfg.IPFSConnector.IPFSHTTP.NodeMultiaddress = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsPort)

	// Save configuration
	if err := cm.saveConfig(serviceJSONPath, cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	cm.logger.Info("IPFS Cluster configuration ensured",
		zap.String("path", serviceJSONPath),
		zap.String("node_name", nodeName),
		zap.Int("ipfs_port", ipfsPort),
		zap.Int("cluster_port", clusterPort),
		zap.Int("rest_api_port", restAPIPort))

	return nil
}

// UpdateBootstrapPeers updates peer_addresses and peerstore with bootstrap peer information
func (cm *ClusterConfigManager) UpdateBootstrapPeers(bootstrapAPIURL string) error {
	if cm.cfg.Database.IPFS.ClusterAPIURL == "" {
		return nil // IPFS not configured
	}

	// Skip if this is the bootstrap node itself
	if cm.cfg.Node.Type == "bootstrap" {
		return nil
	}

	// Query bootstrap cluster API to get peer ID
	peerID, err := getBootstrapPeerID(bootstrapAPIURL)
	if err != nil {
		return fmt.Errorf("failed to get bootstrap peer ID: %w", err)
	}

	if peerID == "" {
		cm.logger.Warn("Bootstrap peer ID not available yet")
		return nil
	}

	// Extract bootstrap cluster port from URL
	_, clusterPort, err := parseClusterPorts(bootstrapAPIURL)
	if err != nil {
		return fmt.Errorf("failed to parse bootstrap cluster API URL: %w", err)
	}

	// Bootstrap listens on clusterPort + 2 (same pattern)
	bootstrapClusterPort := clusterPort + 2
	bootstrapPeerAddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", bootstrapClusterPort, peerID)

	// Load current config
	serviceJSONPath := filepath.Join(cm.clusterPath, "service.json")
	cfg, err := cm.loadOrCreateConfig(serviceJSONPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Update peer_addresses
	cfg.Cluster.PeerAddresses = []string{bootstrapPeerAddr}

	// Save config
	if err := cm.saveConfig(serviceJSONPath, cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// Write to peerstore file
	peerstorePath := filepath.Join(cm.clusterPath, "peerstore")
	if err := os.WriteFile(peerstorePath, []byte(bootstrapPeerAddr+"\n"), 0644); err != nil {
		return fmt.Errorf("failed to write peerstore: %w", err)
	}

	cm.logger.Info("Updated bootstrap peer configuration",
		zap.String("bootstrap_peer_addr", bootstrapPeerAddr),
		zap.String("peerstore_path", peerstorePath))

	return nil
}

// loadOrCreateConfig loads existing service.json or creates a template
func (cm *ClusterConfigManager) loadOrCreateConfig(path string) (*ClusterServiceConfig, error) {
	// Try to load existing config
	if data, err := os.ReadFile(path); err == nil {
		var cfg ClusterServiceConfig
		if err := json.Unmarshal(data, &cfg); err == nil {
			// Also unmarshal into raw map to preserve all fields
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err == nil {
				cfg.Raw = raw
			}
			return &cfg, nil
		}
	}

	// Create new config from template
	return cm.createTemplateConfig(), nil
}

// createTemplateConfig creates a template configuration matching the structure
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

// saveConfig saves the configuration, preserving all existing fields
func (cm *ClusterConfigManager) saveConfig(path string, cfg *ClusterServiceConfig) error {
	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create cluster directory: %w", err)
	}

	// Load existing config if it exists to preserve all fields
	var final map[string]interface{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &final); err != nil {
			// If parsing fails, start fresh
			final = make(map[string]interface{})
		}
	} else {
		final = make(map[string]interface{})
	}

	// Deep merge: update nested structures while preserving other fields
	updateNestedMap(final, "cluster", map[string]interface{}{
		"peername":            cfg.Cluster.Peername,
		"secret":              cfg.Cluster.Secret,
		"leave_on_shutdown":   cfg.Cluster.LeaveOnShutdown,
		"listen_multiaddress": cfg.Cluster.ListenMultiaddress,
		"peer_addresses":      cfg.Cluster.PeerAddresses,
	})

	updateNestedMap(final, "consensus", map[string]interface{}{
		"crdt": map[string]interface{}{
			"cluster_name":  cfg.Consensus.CRDT.ClusterName,
			"trusted_peers": cfg.Consensus.CRDT.TrustedPeers,
			"batching": map[string]interface{}{
				"max_batch_size": cfg.Consensus.CRDT.Batching.MaxBatchSize,
				"max_batch_age":  cfg.Consensus.CRDT.Batching.MaxBatchAge,
			},
			"repair_interval": cfg.Consensus.CRDT.RepairInterval,
		},
	})

	// Update API section, preserving other fields
	updateNestedMap(final, "api", map[string]interface{}{
		"ipfsproxy": map[string]interface{}{
			"listen_multiaddress": cfg.API.IPFSProxy.ListenMultiaddress,
			"node_multiaddress":   cfg.API.IPFSProxy.NodeMultiaddress, // FIX: Correct path!
		},
		"pinsvcapi": map[string]interface{}{
			"http_listen_multiaddress": cfg.API.PinSvcAPI.HTTPListenMultiaddress,
		},
		"restapi": map[string]interface{}{
			"http_listen_multiaddress": cfg.API.RestAPI.HTTPListenMultiaddress,
		},
	})

	// Update IPFS connector section
	updateNestedMap(final, "ipfs_connector", map[string]interface{}{
		"ipfshttp": map[string]interface{}{
			"node_multiaddress":         cfg.IPFSConnector.IPFSHTTP.NodeMultiaddress,
			"connect_swarms_delay":      "30s",
			"ipfs_request_timeout":      "5m0s",
			"pin_timeout":               "2m0s",
			"unpin_timeout":             "3h0m0s",
			"repogc_timeout":            "24h0m0s",
			"informer_trigger_interval": 0,
		},
	})

	// Ensure all required sections exist with defaults if missing
	ensureRequiredSection(final, "datastore", map[string]interface{}{
		"pebble": map[string]interface{}{
			"pebble_options": map[string]interface{}{
				"cache_size_bytes": 1073741824,
				"bytes_per_sync":   1048576,
				"disable_wal":      false,
			},
		},
	})

	ensureRequiredSection(final, "informer", map[string]interface{}{
		"disk": map[string]interface{}{
			"metric_ttl":  "30s",
			"metric_type": "freespace",
		},
		"pinqueue": map[string]interface{}{
			"metric_ttl":         "30s",
			"weight_bucket_size": 100000,
		},
		"tags": map[string]interface{}{
			"metric_ttl": "30s",
			"tags": map[string]interface{}{
				"group": "default",
			},
		},
	})

	ensureRequiredSection(final, "monitor", map[string]interface{}{
		"pubsubmon": map[string]interface{}{
			"check_interval": "15s",
		},
	})

	ensureRequiredSection(final, "pin_tracker", map[string]interface{}{
		"stateless": map[string]interface{}{
			"concurrent_pins":          10,
			"priority_pin_max_age":     "24h0m0s",
			"priority_pin_max_retries": 5,
		},
	})

	ensureRequiredSection(final, "allocator", map[string]interface{}{
		"balanced": map[string]interface{}{
			"allocate_by": []interface{}{"tag:group", "freespace"},
		},
	})

	// Write JSON
	data, err := json.MarshalIndent(final, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// updateNestedMap updates a nested map structure, merging values
func updateNestedMap(parent map[string]interface{}, key string, updates map[string]interface{}) {
	existing, ok := parent[key].(map[string]interface{})
	if !ok {
		parent[key] = updates
		return
	}

	// Merge updates into existing
	for k, v := range updates {
		if vm, ok := v.(map[string]interface{}); ok {
			// Recursively merge nested maps
			if _, ok := existing[k].(map[string]interface{}); !ok {
				existing[k] = vm
			} else {
				updateNestedMap(existing, k, vm)
			}
		} else {
			existing[k] = v
		}
	}
	parent[key] = existing
}

// ensureRequiredSection ensures a section exists in the config, creating it with defaults if missing
func ensureRequiredSection(parent map[string]interface{}, key string, defaults map[string]interface{}) {
	if _, exists := parent[key]; !exists {
		parent[key] = defaults
		return
	}
	// If section exists, merge defaults to ensure all required subsections exist
	existing, ok := parent[key].(map[string]interface{})
	if ok {
		updateNestedMap(parent, key, defaults)
		parent[key] = existing
	}
}

// parseClusterPorts extracts cluster port and REST API port from ClusterAPIURL
func parseClusterPorts(clusterAPIURL string) (clusterPort, restAPIPort int, err error) {
	u, err := url.Parse(clusterAPIURL)
	if err != nil {
		return 0, 0, err
	}

	portStr := u.Port()
	if portStr == "" {
		// Default port based on scheme
		if u.Scheme == "http" {
			portStr = "9094"
		} else if u.Scheme == "https" {
			portStr = "443"
		} else {
			return 0, 0, fmt.Errorf("unknown scheme: %s", u.Scheme)
		}
	}

	_, err = fmt.Sscanf(portStr, "%d", &restAPIPort)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port: %s", portStr)
	}

	// Cluster listen port is typically REST API port + 2
	clusterPort = restAPIPort + 2

	return clusterPort, restAPIPort, nil
}

// parseIPFSPort extracts IPFS API port from APIURL
func parseIPFSPort(apiURL string) (int, error) {
	if apiURL == "" {
		return 5001, nil // Default
	}

	u, err := url.Parse(apiURL)
	if err != nil {
		return 0, err
	}

	portStr := u.Port()
	if portStr == "" {
		if u.Scheme == "http" {
			return 5001, nil // Default HTTP port
		}
		return 0, fmt.Errorf("unknown scheme: %s", u.Scheme)
	}

	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		return 0, fmt.Errorf("invalid port: %s", portStr)
	}

	return port, nil
}

// getBootstrapPeerID queries the bootstrap cluster API to get the peer ID
func getBootstrapPeerID(apiURL string) (string, error) {
	// Simple HTTP client to query /peers endpoint
	client := &standardHTTPClient{}
	peersResp, err := client.Get(fmt.Sprintf("%s/peers", apiURL))
	if err != nil {
		return "", err
	}

	var peersData struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(peersResp, &peersData); err != nil {
		return "", err
	}

	return peersData.ID, nil
}

// loadOrGenerateClusterSecret loads cluster secret or generates a new one
func loadOrGenerateClusterSecret(path string) (string, error) {
	// Try to load existing secret
	if data, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(data)), nil
	}

	// Generate new secret (32 bytes hex = 64 hex chars)
	secret := generateRandomSecret(64)

	// Save secret
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		return "", err
	}

	return secret, nil
}

// generateRandomSecret generates a random hex string
func generateRandomSecret(length int) string {
	bytes := make([]byte, length/2)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to simple generation if crypto/rand fails
		for i := range bytes {
			bytes[i] = byte(os.Getpid() + i)
		}
	}
	return hex.EncodeToString(bytes)
}

// standardHTTPClient implements HTTP client using net/http
type standardHTTPClient struct{}

func (c *standardHTTPClient) Get(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// FixIPFSConfigAddresses fixes localhost addresses in IPFS config to use 127.0.0.1
// This is necessary because IPFS doesn't accept "localhost" as a valid IP address in multiaddrs
// This function always ensures the config is correct, regardless of current state
func (cm *ClusterConfigManager) FixIPFSConfigAddresses() error {
	if cm.cfg.Database.IPFS.APIURL == "" {
		return nil // IPFS not configured
	}

	// Determine IPFS repo path from config
	dataDir := cm.cfg.Node.DataDir
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}

	// Try to find IPFS repo path
	// Check common locations: dataDir/ipfs/repo, or dataDir/bootstrap/ipfs/repo, etc.
	possiblePaths := []string{
		filepath.Join(dataDir, "ipfs", "repo"),
		filepath.Join(dataDir, "bootstrap", "ipfs", "repo"),
		filepath.Join(dataDir, "node2", "ipfs", "repo"),
		filepath.Join(dataDir, "node3", "ipfs", "repo"),
		filepath.Join(filepath.Dir(dataDir), "bootstrap", "ipfs", "repo"),
		filepath.Join(filepath.Dir(dataDir), "node2", "ipfs", "repo"),
		filepath.Join(filepath.Dir(dataDir), "node3", "ipfs", "repo"),
	}

	var ipfsRepoPath string
	for _, path := range possiblePaths {
		if _, err := os.Stat(filepath.Join(path, "config")); err == nil {
			ipfsRepoPath = path
			break
		}
	}

	if ipfsRepoPath == "" {
		cm.logger.Debug("IPFS repo not found, skipping config fix")
		return nil // Not an error if repo doesn't exist yet
	}

	// Parse IPFS API port from config
	ipfsPort, err := parseIPFSPort(cm.cfg.Database.IPFS.APIURL)
	if err != nil {
		return fmt.Errorf("failed to parse IPFS API URL: %w", err)
	}

	// Determine gateway port (typically API port + 3079, or 8080 for bootstrap, 8081 for node2, etc.)
	gatewayPort := 8080
	if strings.Contains(dataDir, "node2") {
		gatewayPort = 8081
	} else if strings.Contains(dataDir, "node3") {
		gatewayPort = 8082
	} else if ipfsPort == 5002 {
		gatewayPort = 8081
	} else if ipfsPort == 5003 {
		gatewayPort = 8082
	}

	// Always ensure API address is correct (don't just check, always set it)
	correctAPIAddr := fmt.Sprintf(`["/ip4/127.0.0.1/tcp/%d"]`, ipfsPort)
	cm.logger.Info("Ensuring IPFS API address is correct",
		zap.String("repo", ipfsRepoPath),
		zap.Int("port", ipfsPort),
		zap.String("correct_address", correctAPIAddr))

	fixCmd := exec.Command("ipfs", "config", "--json", "Addresses.API", correctAPIAddr)
	fixCmd.Env = append(os.Environ(), "IPFS_PATH="+ipfsRepoPath)
	if err := fixCmd.Run(); err != nil {
		cm.logger.Warn("Failed to fix IPFS API address", zap.Error(err))
		return fmt.Errorf("failed to set IPFS API address: %w", err)
	}

	// Always ensure Gateway address is correct
	correctGatewayAddr := fmt.Sprintf(`["/ip4/127.0.0.1/tcp/%d"]`, gatewayPort)
	cm.logger.Info("Ensuring IPFS Gateway address is correct",
		zap.String("repo", ipfsRepoPath),
		zap.Int("port", gatewayPort),
		zap.String("correct_address", correctGatewayAddr))

	fixCmd = exec.Command("ipfs", "config", "--json", "Addresses.Gateway", correctGatewayAddr)
	fixCmd.Env = append(os.Environ(), "IPFS_PATH="+ipfsRepoPath)
	if err := fixCmd.Run(); err != nil {
		cm.logger.Warn("Failed to fix IPFS Gateway address", zap.Error(err))
		return fmt.Errorf("failed to set IPFS Gateway address: %w", err)
	}

	// Check if IPFS daemon is running - if so, it may need to be restarted for changes to take effect
	// We can't restart it from here (it's managed by Makefile/systemd), but we can warn
	if cm.isIPFSRunning(ipfsPort) {
		cm.logger.Warn("IPFS daemon appears to be running - it may need to be restarted for config changes to take effect",
			zap.Int("port", ipfsPort),
			zap.String("repo", ipfsRepoPath))
	}

	return nil
}

// isIPFSRunning checks if IPFS daemon is running by attempting to connect to the API
func (cm *ClusterConfigManager) isIPFSRunning(port int) bool {
	client := &http.Client{
		Timeout: 1 * time.Second,
	}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v0/id", port))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}
