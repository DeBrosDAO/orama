package install

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/curve25519"

	"github.com/DeBrosOfficial/network/pkg/wireguard"
)

// WireGuardPeer represents a WireGuard mesh peer
type WireGuardPeer = wireguard.Peer

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
	sb.WriteString("MTU = 1420\n")

	// Accept all WireGuard subnet traffic before UFW's conntrack "invalid" drop.
	// Without this, packets reordered by the tunnel get silently dropped.
	sb.WriteString("PostUp = iptables -I INPUT 1 -i wg0 -s 10.0.0.0/24 -j ACCEPT\n")
	sb.WriteString("PostDown = iptables -D INPUT -i wg0 -s 10.0.0.0/24 -j ACCEPT\n")

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

	if err := os.MkdirAll(wp.configDir, 0700); err == nil {
		if err := os.WriteFile(confPath, []byte(content), 0600); err == nil {
			return forcePrivateMode(confPath)
		}
	}

	cmd := exec.Command("tee", confPath)
	cmd.Stdin = strings.NewReader(content)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to write wg0.conf via tee: %w\n%s", err, string(output))
	}
	return forcePrivateMode(confPath)
}

// forcePrivateMode chmod 0600 and verifies the result (bugboard #247).
// os.WriteFile's mode is umask-masked; tee inherits umask (often 0644).
func forcePrivateMode(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod 0600 %s: %w", path, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if fi.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s mode %o, want 0600", path, fi.Mode().Perm())
	}
	return nil
}

// Enable brings wg0 up from the existing conf. It does not enable
// wg-quick@wg0 — orama-node starts orama-namespace-wireguard@index, which
// runs `wg-quick up` without rewriting wg0.conf. If the interface is already
// up, this is a no-op (never bounce the mesh).
func (wp *WireGuardProvisioner) Enable() error {
	if exec.Command("wg", "show", WireGuardInterface).Run() == nil {
		return nil
	}
	cmd := exec.Command("wg-quick", "up", WireGuardInterface)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start wg0: %w\n%s", err, string(output))
	}
	return nil
}
