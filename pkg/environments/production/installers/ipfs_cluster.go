package installers

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IPFSClusterInstaller handles IPFS Cluster Service installation
type IPFSClusterInstaller struct {
	*BaseInstaller
}

// NewIPFSClusterInstaller creates a new IPFS Cluster installer
func NewIPFSClusterInstaller(arch string, logWriter io.Writer) *IPFSClusterInstaller {
	return &IPFSClusterInstaller{
		BaseInstaller: NewBaseInstaller(arch, logWriter),
	}
}

// IsInstalled checks if IPFS Cluster is already installed
func (ici *IPFSClusterInstaller) IsInstalled() bool {
	_, err := exec.LookPath("ipfs-cluster-service")
	return err == nil
}

// Install downloads and installs IPFS Cluster Service
func (ici *IPFSClusterInstaller) Install() error {
	if ici.IsInstalled() {
		fmt.Fprintf(ici.logWriter, "  ✓ IPFS Cluster already installed\n")
		return nil
	}

	fmt.Fprintf(ici.logWriter, "  Installing IPFS Cluster Service...\n")

	// Check if Go is available
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go not found - required to install IPFS Cluster. Please install Go first")
	}

	cmd := exec.Command("go", "install", "github.com/ipfs-cluster/ipfs-cluster/cmd/ipfs-cluster-service@latest")
	cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install IPFS Cluster: %w", err)
	}

	fmt.Fprintf(ici.logWriter, "  ✓ IPFS Cluster installed\n")
	return nil
}

// Configure is a placeholder for IPFS Cluster configuration
func (ici *IPFSClusterInstaller) Configure() error {
	// Configuration is handled by InitializeConfig
	return nil
}

// InitializeConfig initializes IPFS Cluster configuration (unified - no bootstrap/node distinction)
// This runs `ipfs-cluster-service init` to create the service.json configuration file.
// For existing installations, it ensures the cluster secret is up to date.
// clusterPeers should be in format: ["/ip4/<ip>/tcp/9100/p2p/<cluster-peer-id>"]
func (ici *IPFSClusterInstaller) InitializeConfig(clusterPath, clusterSecret string, ipfsAPIPort int, clusterPeers []string) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")
	configExists := false
	if _, err := os.Stat(serviceJSONPath); err == nil {
		configExists = true
		fmt.Fprintf(ici.logWriter, "    IPFS Cluster config already exists, ensuring it's up to date...\n")
	} else {
		fmt.Fprintf(ici.logWriter, "    Preparing IPFS Cluster path...\n")
	}

	if err := os.MkdirAll(clusterPath, 0755); err != nil {
		return fmt.Errorf("failed to create IPFS Cluster directory: %w", err)
	}

	// Fix ownership before running init (best-effort)
	if err := exec.Command("chown", "-R", "debros:debros", clusterPath).Run(); err != nil {
		fmt.Fprintf(ici.logWriter, "    ⚠️  Warning: failed to chown cluster path before init: %v\n", err)
	}

	// Resolve ipfs-cluster-service binary path
	clusterBinary, err := ResolveBinaryPath("ipfs-cluster-service", "/usr/local/bin/ipfs-cluster-service", "/usr/bin/ipfs-cluster-service")
	if err != nil {
		return fmt.Errorf("ipfs-cluster-service binary not found: %w", err)
	}

	// Initialize cluster config if it doesn't exist
	if !configExists {
		// Initialize cluster config with ipfs-cluster-service init
		// This creates the service.json file with all required sections
		fmt.Fprintf(ici.logWriter, "    Initializing IPFS Cluster config...\n")
		cmd := exec.Command(clusterBinary, "init", "--force")
		cmd.Env = append(os.Environ(), "IPFS_CLUSTER_PATH="+clusterPath)
		// Pass CLUSTER_SECRET to init so it writes the correct secret to service.json directly
		if clusterSecret != "" {
			cmd.Env = append(cmd.Env, "CLUSTER_SECRET="+clusterSecret)
		}
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to initialize IPFS Cluster config: %v\n%s", err, string(output))
		}
	}

	// Always update the cluster secret, IPFS port, and peer addresses (for both new and existing configs)
	// This ensures existing installations get the secret and port synchronized
	// We do this AFTER init to ensure our secret takes precedence
	if clusterSecret != "" {
		fmt.Fprintf(ici.logWriter, "    Updating cluster secret, IPFS port, and peer addresses...\n")
		if err := ici.updateConfig(clusterPath, clusterSecret, ipfsAPIPort, clusterPeers); err != nil {
			return fmt.Errorf("failed to update cluster config: %w", err)
		}

		// Verify the secret was written correctly
		if err := ici.verifySecret(clusterPath, clusterSecret); err != nil {
			return fmt.Errorf("cluster secret verification failed: %w", err)
		}
		fmt.Fprintf(ici.logWriter, "    ✓ Cluster secret verified\n")
	}

	// Fix ownership again after updates (best-effort)
	if err := exec.Command("chown", "-R", "debros:debros", clusterPath).Run(); err != nil {
		fmt.Fprintf(ici.logWriter, "    ⚠️  Warning: failed to chown cluster path after updates: %v\n", err)
	}

	return nil
}

// updateConfig updates the secret, IPFS port, and peer addresses in IPFS Cluster service.json
func (ici *IPFSClusterInstaller) updateConfig(clusterPath, secret string, ipfsAPIPort int, bootstrapClusterPeers []string) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")

	// Read existing config
	data, err := os.ReadFile(serviceJSONPath)
	if err != nil {
		return fmt.Errorf("failed to read service.json: %w", err)
	}

	// Parse JSON
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse service.json: %w", err)
	}

	// Update cluster secret, listen_multiaddress, and peer addresses
	if cluster, ok := config["cluster"].(map[string]interface{}); ok {
		cluster["secret"] = secret
		// Set consistent listen_multiaddress - port 9100 for cluster LibP2P communication
		// This MUST match the port used in GetClusterPeerMultiaddr() and peer_addresses
		cluster["listen_multiaddress"] = []interface{}{"/ip4/0.0.0.0/tcp/9100"}
		// Configure peer addresses for cluster discovery
		// This allows nodes to find and connect to each other
		// Merge new peers with existing peers (preserves manually configured peers)
		if len(bootstrapClusterPeers) > 0 {
			existingPeers := ici.extractExistingPeers(cluster)
			mergedPeers := ici.mergePeerAddresses(existingPeers, bootstrapClusterPeers)
			cluster["peer_addresses"] = mergedPeers
		}
		// If no new peers provided, preserve existing peer_addresses (don't overwrite)
	} else {
		clusterConfig := map[string]interface{}{
			"secret":              secret,
			"listen_multiaddress": []interface{}{"/ip4/0.0.0.0/tcp/9100"},
		}
		if len(bootstrapClusterPeers) > 0 {
			clusterConfig["peer_addresses"] = bootstrapClusterPeers
		}
		config["cluster"] = clusterConfig
	}

	// Update IPFS port in IPFS Proxy configuration
	ipfsNodeMultiaddr := fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", ipfsAPIPort)
	if api, ok := config["api"].(map[string]interface{}); ok {
		if ipfsproxy, ok := api["ipfsproxy"].(map[string]interface{}); ok {
			ipfsproxy["node_multiaddress"] = ipfsNodeMultiaddr
		}
	}

	// Update IPFS port in IPFS Connector configuration
	if ipfsConnector, ok := config["ipfs_connector"].(map[string]interface{}); ok {
		if ipfshttp, ok := ipfsConnector["ipfshttp"].(map[string]interface{}); ok {
			ipfshttp["node_multiaddress"] = ipfsNodeMultiaddr
		}
	}

	// Write back
	updatedData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal service.json: %w", err)
	}

	if err := os.WriteFile(serviceJSONPath, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write service.json: %w", err)
	}

	return nil
}

// extractExistingPeers extracts existing peer addresses from cluster config
func (ici *IPFSClusterInstaller) extractExistingPeers(cluster map[string]interface{}) []string {
	var peers []string
	if peerAddrs, ok := cluster["peer_addresses"].([]interface{}); ok {
		for _, addr := range peerAddrs {
			if addrStr, ok := addr.(string); ok && addrStr != "" {
				peers = append(peers, addrStr)
			}
		}
	}
	return peers
}

// mergePeerAddresses merges existing and new peer addresses, removing duplicates
func (ici *IPFSClusterInstaller) mergePeerAddresses(existing, new []string) []string {
	seen := make(map[string]bool)
	var merged []string

	// Add existing peers first
	for _, peer := range existing {
		if !seen[peer] {
			seen[peer] = true
			merged = append(merged, peer)
		}
	}

	// Add new peers (if not already present)
	for _, peer := range new {
		if !seen[peer] {
			seen[peer] = true
			merged = append(merged, peer)
		}
	}

	return merged
}

// verifySecret verifies that the secret in service.json matches the expected value
func (ici *IPFSClusterInstaller) verifySecret(clusterPath, expectedSecret string) error {
	serviceJSONPath := filepath.Join(clusterPath, "service.json")

	data, err := os.ReadFile(serviceJSONPath)
	if err != nil {
		return fmt.Errorf("failed to read service.json for verification: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse service.json for verification: %w", err)
	}

	if cluster, ok := config["cluster"].(map[string]interface{}); ok {
		if secret, ok := cluster["secret"].(string); ok {
			if secret != expectedSecret {
				return fmt.Errorf("secret mismatch: expected %s, got %s", expectedSecret, secret)
			}
			return nil
		}
		return fmt.Errorf("secret not found in cluster config")
	}

	return fmt.Errorf("cluster section not found in service.json")
}

// GetClusterPeerMultiaddr reads the IPFS Cluster peer ID and returns its multiaddress
// Returns format: /ip4/<ip>/tcp/9100/p2p/<cluster-peer-id>
func (ici *IPFSClusterInstaller) GetClusterPeerMultiaddr(clusterPath string, nodeIP string) (string, error) {
	identityPath := filepath.Join(clusterPath, "identity.json")

	// Read identity file
	data, err := os.ReadFile(identityPath)
	if err != nil {
		return "", fmt.Errorf("failed to read identity.json: %w", err)
	}

	// Parse JSON
	var identity map[string]interface{}
	if err := json.Unmarshal(data, &identity); err != nil {
		return "", fmt.Errorf("failed to parse identity.json: %w", err)
	}

	// Get peer ID
	peerID, ok := identity["id"].(string)
	if !ok || peerID == "" {
		return "", fmt.Errorf("peer ID not found in identity.json")
	}

	// Construct multiaddress: /ip4/<ip>/tcp/9100/p2p/<peer-id>
	// Port 9100 is the cluster listen port for libp2p communication
	multiaddr := fmt.Sprintf("/ip4/%s/tcp/9100/p2p/%s", nodeIP, peerID)
	return multiaddr, nil
}

// inferPeerIP extracts the IP address from peer addresses
func inferPeerIP(peerAddresses []string, vpsIP string) string {
	for _, addr := range peerAddresses {
		// Look for /ip4/ prefix
		if strings.Contains(addr, "/ip4/") {
			parts := strings.Split(addr, "/")
			for i, part := range parts {
				if part == "ip4" && i+1 < len(parts) {
					return parts[i+1]
				}
			}
		}
	}
	return vpsIP // Fallback to VPS IP
}
