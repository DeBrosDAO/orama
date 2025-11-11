package development

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/encryption"
	"github.com/DeBrosOfficial/network/pkg/environments/templates"
)

// ConfigEnsurer handles all config file creation and validation
type ConfigEnsurer struct {
	debrosDir string
}

// NewConfigEnsurer creates a new config ensurer
func NewConfigEnsurer(debrosDir string) *ConfigEnsurer {
	return &ConfigEnsurer{
		debrosDir: debrosDir,
	}
}

// EnsureAll ensures all necessary config files and secrets exist
func (ce *ConfigEnsurer) EnsureAll() error {
	// Create directories
	if err := os.MkdirAll(ce.debrosDir, 0755); err != nil {
		return fmt.Errorf("failed to create .debros directory: %w", err)
	}

	if err := os.MkdirAll(filepath.Join(ce.debrosDir, "logs"), 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Ensure shared secrets
	if err := ce.ensureSharedSecrets(); err != nil {
		return fmt.Errorf("failed to ensure shared secrets: %w", err)
	}

	// Load topology
	topology := DefaultTopology()

	// Generate identities for all bootstrap nodes and collect multiaddrs
	bootstrapAddrs := []string{}
	for _, nodeSpec := range topology.GetBootstrapNodes() {
		addr, err := ce.ensureNodeIdentity(nodeSpec)
		if err != nil {
			return fmt.Errorf("failed to ensure identity for %s: %w", nodeSpec.Name, err)
		}
		bootstrapAddrs = append(bootstrapAddrs, addr)
	}

	// Ensure configs for all bootstrap and regular nodes
	for _, nodeSpec := range topology.Nodes {
		if err := ce.ensureNodeConfig(nodeSpec, bootstrapAddrs); err != nil {
			return fmt.Errorf("failed to ensure config for %s: %w", nodeSpec.Name, err)
		}
	}

	// Ensure gateway config
	if err := ce.ensureGateway(bootstrapAddrs); err != nil {
		return fmt.Errorf("failed to ensure gateway: %w", err)
	}

	// Ensure Olric config
	if err := ce.ensureOlric(); err != nil {
		return fmt.Errorf("failed to ensure olric: %w", err)
	}

	return nil
}

// ensureSharedSecrets creates cluster secret and swarm key if they don't exist
func (ce *ConfigEnsurer) ensureSharedSecrets() error {
	secretPath := filepath.Join(ce.debrosDir, "cluster-secret")
	if _, err := os.Stat(secretPath); os.IsNotExist(err) {
		secret := generateRandomHex(64) // 64 hex chars = 32 bytes
		if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
			return fmt.Errorf("failed to write cluster secret: %w", err)
		}
		fmt.Printf("✓ Generated cluster secret\n")
	}

	swarmKeyPath := filepath.Join(ce.debrosDir, "swarm.key")
	if _, err := os.Stat(swarmKeyPath); os.IsNotExist(err) {
		keyHex := strings.ToUpper(generateRandomHex(64))
		content := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", keyHex)
		if err := os.WriteFile(swarmKeyPath, []byte(content), 0600); err != nil {
			return fmt.Errorf("failed to write swarm key: %w", err)
		}
		fmt.Printf("✓ Generated IPFS swarm key\n")
	}

	return nil
}

// ensureNodeIdentity creates or loads a node identity and returns its multiaddr
func (ce *ConfigEnsurer) ensureNodeIdentity(nodeSpec NodeSpec) (string, error) {
	nodeDir := filepath.Join(ce.debrosDir, nodeSpec.DataDir)
	identityPath := filepath.Join(nodeDir, "identity.key")

	// Create identity if missing
	var peerID string
	if _, err := os.Stat(identityPath); os.IsNotExist(err) {
		if err := os.MkdirAll(nodeDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create node directory: %w", err)
		}

		info, err := encryption.GenerateIdentity()
		if err != nil {
			return "", fmt.Errorf("failed to generate identity: %w", err)
		}

		if err := encryption.SaveIdentity(info, identityPath); err != nil {
			return "", fmt.Errorf("failed to save identity: %w", err)
		}

		peerID = info.PeerID.String()
		fmt.Printf("✓ Generated %s identity (Peer ID: %s)\n", nodeSpec.Name, peerID)
	} else {
		info, err := encryption.LoadIdentity(identityPath)
		if err != nil {
			return "", fmt.Errorf("failed to load identity: %w", err)
		}
		peerID = info.PeerID.String()
	}

	// Return multiaddr
	return fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", nodeSpec.P2PPort, peerID), nil
}

// ensureNodeConfig creates or updates a node configuration
func (ce *ConfigEnsurer) ensureNodeConfig(nodeSpec NodeSpec, bootstrapAddrs []string) error {
	nodeDir := filepath.Join(ce.debrosDir, nodeSpec.DataDir)
	configPath := filepath.Join(ce.debrosDir, nodeSpec.ConfigFilename)

	if err := os.MkdirAll(nodeDir, 0755); err != nil {
		return fmt.Errorf("failed to create node directory: %w", err)
	}

	if nodeSpec.Role == "bootstrap" {
		// Generate bootstrap config
		data := templates.BootstrapConfigData{
			NodeID:            nodeSpec.Name,
			P2PPort:           nodeSpec.P2PPort,
			DataDir:           nodeDir,
			RQLiteHTTPPort:    nodeSpec.RQLiteHTTPPort,
			RQLiteRaftPort:    nodeSpec.RQLiteRaftPort,
			ClusterAPIPort:    nodeSpec.ClusterAPIPort,
			IPFSAPIPort:       nodeSpec.IPFSAPIPort,
			BootstrapPeers:    bootstrapAddrs,
			RQLiteJoinAddress: nodeSpec.RQLiteJoinTarget,
		}

		config, err := templates.RenderBootstrapConfig(data)
		if err != nil {
			return fmt.Errorf("failed to render bootstrap config: %w", err)
		}

		if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
			return fmt.Errorf("failed to write bootstrap config: %w", err)
		}

		fmt.Printf("✓ Generated %s.yaml\n", nodeSpec.Name)
	} else {
		// Generate regular node config
		data := templates.NodeConfigData{
			NodeID:            nodeSpec.Name,
			P2PPort:           nodeSpec.P2PPort,
			DataDir:           nodeDir,
			RQLiteHTTPPort:    nodeSpec.RQLiteHTTPPort,
			RQLiteRaftPort:    nodeSpec.RQLiteRaftPort,
			RQLiteJoinAddress: nodeSpec.RQLiteJoinTarget,
			BootstrapPeers:    bootstrapAddrs,
			ClusterAPIPort:    nodeSpec.ClusterAPIPort,
			IPFSAPIPort:       nodeSpec.IPFSAPIPort,
		}

		config, err := templates.RenderNodeConfig(data)
		if err != nil {
			return fmt.Errorf("failed to render node config: %w", err)
		}

		if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
			return fmt.Errorf("failed to write node config: %w", err)
		}

		fmt.Printf("✓ Generated %s.yaml\n", nodeSpec.Name)
	}

	return nil
}

// ensureGateway creates gateway config
func (ce *ConfigEnsurer) ensureGateway(bootstrapAddrs []string) error {
	configPath := filepath.Join(ce.debrosDir, "gateway.yaml")

	// Get first bootstrap's cluster API port for default
	topology := DefaultTopology()
	firstBootstrap := topology.GetBootstrapNodes()[0]

	data := templates.GatewayConfigData{
		ListenPort:     topology.GatewayPort,
		BootstrapPeers: bootstrapAddrs,
		OlricServers:   []string{fmt.Sprintf("127.0.0.1:%d", topology.OlricHTTPPort)},
		ClusterAPIPort: firstBootstrap.ClusterAPIPort,
		IPFSAPIPort:    firstBootstrap.IPFSAPIPort,
	}

	config, err := templates.RenderGatewayConfig(data)
	if err != nil {
		return fmt.Errorf("failed to render gateway config: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write gateway config: %w", err)
	}

	fmt.Printf("✓ Generated gateway.yaml\n")
	return nil
}

// ensureOlric creates Olric config
func (ce *ConfigEnsurer) ensureOlric() error {
	configPath := filepath.Join(ce.debrosDir, "olric-config.yaml")

	topology := DefaultTopology()
	data := templates.OlricConfigData{
		BindAddr:       "127.0.0.1",
		HTTPPort:       topology.OlricHTTPPort,
		MemberlistPort: topology.OlricMemberPort,
	}

	config, err := templates.RenderOlricConfig(data)
	if err != nil {
		return fmt.Errorf("failed to render olric config: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write olric config: %w", err)
	}

	fmt.Printf("✓ Generated olric-config.yaml\n")
	return nil
}

// generateRandomHex generates a random hex string of specified length
func generateRandomHex(length int) string {
	bytes := make([]byte, length/2)
	if _, err := rand.Read(bytes); err != nil {
		panic(fmt.Sprintf("failed to generate random bytes: %v", err))
	}
	return hex.EncodeToString(bytes)
}
