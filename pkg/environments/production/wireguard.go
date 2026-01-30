package production

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// WireGuardPeer represents a WireGuard mesh peer
type WireGuardPeer struct {
	PublicKey string // Base64-encoded public key
	Endpoint  string // e.g., "141.227.165.154:51820"
	AllowedIP string // e.g., "10.0.0.2/32"
}

// WireGuardConfig holds the configuration for a WireGuard interface
type WireGuardConfig struct {
	PrivateIP  string          // e.g., "10.0.0.1"
	ListenPort int             // default 51820
	PrivateKey string          // Base64-encoded private key
	Peers      []WireGuardPeer // Known peers
}

// WireGuardProvisioner manages WireGuard VPN setup
type WireGuardProvisioner struct {
	configDir string // /etc/wireguard
	config    WireGuardConfig
}

// NewWireGuardProvisioner creates a new WireGuard provisioner
func NewWireGuardProvisioner(config WireGuardConfig) *WireGuardProvisioner {
	if config.ListenPort == 0 {
		config.ListenPort = 51820
	}
	return &WireGuardProvisioner{
		configDir: "/etc/wireguard",
		config:    config,
	}
}

// IsInstalled checks if WireGuard tools are available
func (wp *WireGuardProvisioner) IsInstalled() bool {
	_, err := exec.LookPath("wg")
	return err == nil
}

// Install installs the WireGuard package
func (wp *WireGuardProvisioner) Install() error {
	if wp.IsInstalled() {
		return nil
	}

	cmd := exec.Command("apt-get", "install", "-y", "wireguard", "wireguard-tools")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to install wireguard: %w\n%s", err, string(output))
	}

	return nil
}

// GenerateKeyPair generates a new WireGuard private/public key pair
func GenerateKeyPair() (privateKey, publicKey string, err error) {
	// Generate 32 random bytes for private key
	var privBytes [32]byte
	if _, err := rand.Read(privBytes[:]); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	// Clamp private key per Curve25519 spec
	privBytes[0] &= 248
	privBytes[31] &= 127
	privBytes[31] |= 64

	// Derive public key
	var pubBytes [32]byte
	curve25519.ScalarBaseMult(&pubBytes, &privBytes)

	privateKey = base64.StdEncoding.EncodeToString(privBytes[:])
	publicKey = base64.StdEncoding.EncodeToString(pubBytes[:])
	return privateKey, publicKey, nil
}

// PublicKeyFromPrivate derives the public key from a private key
func PublicKeyFromPrivate(privateKey string) (string, error) {
	privBytes, err := base64.StdEncoding.DecodeString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to decode private key: %w", err)
	}
	if len(privBytes) != 32 {
		return "", fmt.Errorf("invalid private key length: %d", len(privBytes))
	}

	var priv, pub [32]byte
	copy(priv[:], privBytes)
	curve25519.ScalarBaseMult(&pub, &priv)

	return base64.StdEncoding.EncodeToString(pub[:]), nil
}

// GenerateConfig returns the wg0.conf file content
func (wp *WireGuardProvisioner) GenerateConfig() string {
	var sb strings.Builder

	sb.WriteString("# WireGuard mesh configuration (managed by Orama Network)\n")
	sb.WriteString("# Do not edit manually — use orama CLI to manage peers\n\n")
	sb.WriteString("[Interface]\n")
	sb.WriteString(fmt.Sprintf("PrivateKey = %s\n", wp.config.PrivateKey))
	sb.WriteString(fmt.Sprintf("Address = %s/24\n", wp.config.PrivateIP))
	sb.WriteString(fmt.Sprintf("ListenPort = %d\n", wp.config.ListenPort))

	for _, peer := range wp.config.Peers {
		sb.WriteString("\n[Peer]\n")
		sb.WriteString(fmt.Sprintf("PublicKey = %s\n", peer.PublicKey))
		if peer.Endpoint != "" {
			sb.WriteString(fmt.Sprintf("Endpoint = %s\n", peer.Endpoint))
		}
		sb.WriteString(fmt.Sprintf("AllowedIPs = %s\n", peer.AllowedIP))
		sb.WriteString("PersistentKeepalive = 25\n")
	}

	return sb.String()
}

// WriteConfig writes the WireGuard config to /etc/wireguard/wg0.conf
func (wp *WireGuardProvisioner) WriteConfig() error {
	confPath := filepath.Join(wp.configDir, "wg0.conf")
	content := wp.GenerateConfig()

	// Try direct write first (works when running as root)
	if err := os.MkdirAll(wp.configDir, 0700); err == nil {
		if err := os.WriteFile(confPath, []byte(content), 0600); err == nil {
			return nil
		}
	}

	// Fallback to sudo tee (for non-root, e.g. debros user)
	cmd := exec.Command("sudo", "tee", confPath)
	cmd.Stdin = strings.NewReader(content)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write wg0.conf via sudo: %w\n%s", err, string(output))
	}

	return nil
}

// Enable starts and enables the WireGuard interface
func (wp *WireGuardProvisioner) Enable() error {
	// Enable on boot
	cmd := exec.Command("systemctl", "enable", "wg-quick@wg0")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to enable wg-quick@wg0: %w\n%s", err, string(output))
	}

	// Start now
	cmd = exec.Command("systemctl", "start", "wg-quick@wg0")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start wg-quick@wg0: %w\n%s", err, string(output))
	}

	return nil
}

// Restart restarts the WireGuard interface
func (wp *WireGuardProvisioner) Restart() error {
	cmd := exec.Command("systemctl", "restart", "wg-quick@wg0")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to restart wg-quick@wg0: %w\n%s", err, string(output))
	}
	return nil
}

// IsActive checks if the WireGuard interface is up
func (wp *WireGuardProvisioner) IsActive() bool {
	cmd := exec.Command("systemctl", "is-active", "--quiet", "wg-quick@wg0")
	return cmd.Run() == nil
}

// AddPeer adds a peer to the running WireGuard interface without restart
func (wp *WireGuardProvisioner) AddPeer(peer WireGuardPeer) error {
	// Add peer to running interface
	args := []string{"wg", "set", "wg0", "peer", peer.PublicKey, "allowed-ips", peer.AllowedIP, "persistent-keepalive", "25"}
	if peer.Endpoint != "" {
		args = append(args, "endpoint", peer.Endpoint)
	}

	cmd := exec.Command("sudo", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add peer %s: %w\n%s", peer.AllowedIP, err, string(output))
	}

	// Also update config file so it persists across restarts
	wp.config.Peers = append(wp.config.Peers, peer)
	return wp.WriteConfig()
}

// RemovePeer removes a peer from the running WireGuard interface
func (wp *WireGuardProvisioner) RemovePeer(publicKey string) error {
	cmd := exec.Command("sudo", "wg", "set", "wg0", "peer", publicKey, "remove")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to remove peer: %w\n%s", err, string(output))
	}

	// Remove from config
	filtered := make([]WireGuardPeer, 0, len(wp.config.Peers))
	for _, p := range wp.config.Peers {
		if p.PublicKey != publicKey {
			filtered = append(filtered, p)
		}
	}
	wp.config.Peers = filtered
	return wp.WriteConfig()
}

// GetStatus returns the current WireGuard interface status
func (wp *WireGuardProvisioner) GetStatus() (string, error) {
	cmd := exec.Command("wg", "show", "wg0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get wg status: %w\n%s", err, string(output))
	}
	return string(output), nil
}
