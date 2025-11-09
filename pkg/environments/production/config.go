package production

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/environments/templates"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ConfigGenerator manages generation of node, gateway, and service configs
type ConfigGenerator struct {
	debrosDir string
}

// NewConfigGenerator creates a new config generator
func NewConfigGenerator(debrosDir string) *ConfigGenerator {
	return &ConfigGenerator{
		debrosDir: debrosDir,
	}
}

// GenerateNodeConfig generates node.yaml configuration
func (cg *ConfigGenerator) GenerateNodeConfig(isBootstrap bool, bootstrapPeers []string, vpsIP string) (string, error) {
	var nodeID string
	if isBootstrap {
		nodeID = "bootstrap"
	} else {
		nodeID = "node"
	}

	if isBootstrap {
		data := templates.BootstrapConfigData{
			NodeID:         nodeID,
			P2PPort:        4001,
			DataDir:        filepath.Join(cg.debrosDir, "data", "bootstrap"),
			RQLiteHTTPPort: 5001,
			RQLiteRaftPort: 7001,
			ClusterAPIPort: 9094,
			IPFSAPIPort:    4501,
		}
		return templates.RenderBootstrapConfig(data)
	}

	// Regular node
	rqliteJoinAddr := "localhost:7001"
	if vpsIP != "" {
		rqliteJoinAddr = vpsIP + ":7001"
	}

	data := templates.NodeConfigData{
		NodeID:            nodeID,
		P2PPort:           4001,
		DataDir:           filepath.Join(cg.debrosDir, "data", "node"),
		RQLiteHTTPPort:    5001,
		RQLiteRaftPort:    7001,
		RQLiteJoinAddress: rqliteJoinAddr,
		BootstrapPeers:    bootstrapPeers,
		ClusterAPIPort:    9094,
		IPFSAPIPort:       4501,
	}
	return templates.RenderNodeConfig(data)
}

// GenerateGatewayConfig generates gateway.yaml configuration
func (cg *ConfigGenerator) GenerateGatewayConfig(bootstrapPeers []string, enableHTTPS bool, domain string, olricServers []string) (string, error) {
	tlsCacheDir := ""
	if enableHTTPS {
		tlsCacheDir = filepath.Join(cg.debrosDir, "tls-cache")
	}

	data := templates.GatewayConfigData{
		ListenPort:     6001,
		BootstrapPeers: bootstrapPeers,
		OlricServers:   olricServers,
		ClusterAPIPort: 9094,
		IPFSAPIPort:    4501,
		EnableHTTPS:    enableHTTPS,
		DomainName:     domain,
		TLSCacheDir:    tlsCacheDir,
		RQLiteDSN:      "", // Empty for now, can be configured later
	}
	return templates.RenderGatewayConfig(data)
}

// GenerateOlricConfig generates Olric configuration
func (cg *ConfigGenerator) GenerateOlricConfig(bindAddr string, httpPort, memberlistPort int) (string, error) {
	data := templates.OlricConfigData{
		BindAddr:       bindAddr,
		HTTPPort:       httpPort,
		MemberlistPort: memberlistPort,
	}
	return templates.RenderOlricConfig(data)
}

// SecretGenerator manages generation of shared secrets and keys
type SecretGenerator struct {
	debrosDir string
}

// NewSecretGenerator creates a new secret generator
func NewSecretGenerator(debrosDir string) *SecretGenerator {
	return &SecretGenerator{
		debrosDir: debrosDir,
	}
}

// EnsureClusterSecret gets or generates the IPFS Cluster secret
func (sg *SecretGenerator) EnsureClusterSecret() (string, error) {
	secretPath := filepath.Join(sg.debrosDir, "secrets", "cluster-secret")
	secretDir := filepath.Dir(secretPath)

	// Ensure secrets directory exists
	if err := os.MkdirAll(secretDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create secrets directory: %w", err)
	}

	// Try to read existing secret
	if data, err := os.ReadFile(secretPath); err == nil {
		secret := strings.TrimSpace(string(data))
		if len(secret) == 64 {
			return secret, nil
		}
	}

	// Generate new secret (32 bytes = 64 hex chars)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate cluster secret: %w", err)
	}
	secret := hex.EncodeToString(bytes)

	// Write and protect
	if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
		return "", fmt.Errorf("failed to save cluster secret: %w", err)
	}

	return secret, nil
}

// EnsureSwarmKey gets or generates the IPFS private swarm key
func (sg *SecretGenerator) EnsureSwarmKey() ([]byte, error) {
	swarmKeyPath := filepath.Join(sg.debrosDir, "secrets", "swarm.key")
	secretDir := filepath.Dir(swarmKeyPath)

	// Ensure secrets directory exists
	if err := os.MkdirAll(secretDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create secrets directory: %w", err)
	}

	// Try to read existing key
	if data, err := os.ReadFile(swarmKeyPath); err == nil {
		if strings.Contains(string(data), "/key/swarm/psk/1.0.0/") {
			return data, nil
		}
	}

	// Generate new key (32 bytes)
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("failed to generate swarm key: %w", err)
	}

	keyHex := strings.ToUpper(hex.EncodeToString(keyBytes))
	content := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", keyHex)

	// Write and protect
	if err := os.WriteFile(swarmKeyPath, []byte(content), 0600); err != nil {
		return nil, fmt.Errorf("failed to save swarm key: %w", err)
	}

	return []byte(content), nil
}

// EnsureNodeIdentity gets or generates the node's LibP2P identity
func (sg *SecretGenerator) EnsureNodeIdentity(nodeType string) (peer.ID, error) {
	keyDir := filepath.Join(sg.debrosDir, "data", nodeType)
	keyPath := filepath.Join(keyDir, "identity.key")

	// Ensure data directory exists
	if err := os.MkdirAll(keyDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create data directory: %w", err)
	}

	// Try to read existing key
	if data, err := os.ReadFile(keyPath); err == nil {
		priv, err := crypto.UnmarshalPrivateKey(data)
		if err == nil {
			pub := priv.GetPublic()
			peerID, _ := peer.IDFromPublicKey(pub)
			return peerID, nil
		}
	}

	// Generate new identity
	priv, pub, err := crypto.GenerateKeyPair(crypto.Ed25519, 2048)
	if err != nil {
		return "", fmt.Errorf("failed to generate identity: %w", err)
	}

	peerID, _ := peer.IDFromPublicKey(pub)

	// Marshal and save private key
	keyData, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return "", fmt.Errorf("failed to marshal private key: %w", err)
	}

	if err := os.WriteFile(keyPath, keyData, 0600); err != nil {
		return "", fmt.Errorf("failed to save identity key: %w", err)
	}

	return peerID, nil
}

// SaveConfig writes a configuration file to disk
func (sg *SecretGenerator) SaveConfig(filename string, content string) error {
	configDir := filepath.Join(sg.debrosDir, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create configs directory: %w", err)
	}

	configPath := filepath.Join(configDir, filename)
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write config %s: %w", filename, err)
	}

	// Fix ownership
	exec.Command("chown", "debros:debros", configPath).Run()

	return nil
}
