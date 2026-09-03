package production

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/environments/templates"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"gopkg.in/yaml.v3"
)

// defaultSFUSignalingPort is the SFU signaling port the namespace gateway
// proxies WebRTC traffic to when an existing node.yaml did not record one.
// Mirrors pkg/namespace.SFUSignalingPortRangeStart (30000); kept as a local
// constant to avoid importing the namespace package (which other agents own
// and which would create a dependency cycle here).
const defaultSFUSignalingPort = 30000

// ConfigGenerator manages generation of node, gateway, and service configs
type ConfigGenerator struct {
	oramaDir       string
	SSHUser        string // Operator metadata
	Environment    string
	OperatorWallet string
}

// NewConfigGenerator creates a new config generator
func NewConfigGenerator(oramaDir string) *ConfigGenerator {
	return &ConfigGenerator{
		oramaDir: oramaDir,
	}
}

// extractIPFromMultiaddr extracts the IP address from a peer multiaddr
// Supports IP4, IP6, DNS4, DNS6, and DNSADDR protocols
// Returns the IP address as a string, or empty string if extraction/resolution fails
func extractIPFromMultiaddr(multiaddrStr string) string {
	ma, err := multiaddr.NewMultiaddr(multiaddrStr)
	if err != nil {
		return ""
	}

	// First, try to extract direct IP address
	var ip net.IP
	var dnsName string
	multiaddr.ForEach(ma, func(c multiaddr.Component) bool {
		switch c.Protocol().Code {
		case multiaddr.P_IP4, multiaddr.P_IP6:
			ip = net.ParseIP(c.Value())
			return false // Stop iteration - found IP
		case multiaddr.P_DNS4, multiaddr.P_DNS6, multiaddr.P_DNSADDR:
			dnsName = c.Value()
			// Continue to check for IP, but remember DNS name as fallback
		}
		return true
	})

	// If we found a direct IP, return it
	if ip != nil {
		return ip.String()
	}

	// If we found a DNS name, try to resolve it
	if dnsName != "" {
		if resolvedIPs, err := net.LookupIP(dnsName); err == nil && len(resolvedIPs) > 0 {
			// Prefer IPv4 addresses, but accept IPv6 if that's all we have
			for _, resolvedIP := range resolvedIPs {
				if resolvedIP.To4() != nil {
					return resolvedIP.String()
				}
			}
			// Return first IPv6 address if no IPv4 found
			return resolvedIPs[0].String()
		}
	}

	return ""
}

// inferPeerIP extracts the IP address from peer multiaddrs
// Iterates through all peers to find a valid IP (supports DNS resolution)
// Falls back to vpsIP if provided, otherwise returns empty string
func inferPeerIP(peers []string, vpsIP string) string {
	// Try to extract IP from each peer (in order)
	for _, peer := range peers {
		if ip := extractIPFromMultiaddr(peer); ip != "" {
			return ip
		}
	}
	// Fall back to vpsIP if provided
	if vpsIP != "" {
		return vpsIP
	}
	return ""
}

// GenerateNodeConfig generates node.yaml configuration (unified architecture)
func (cg *ConfigGenerator) GenerateNodeConfig(peerAddresses []string, vpsIP string, joinAddress string, domain string, baseDomain string, enableHTTPS bool) (string, error) {
	// Generate node ID from domain or use default
	nodeID := "node"
	if domain != "" {
		// Extract node identifier from domain (e.g., "node-123" from "node-123.orama.network")
		parts := strings.Split(domain, ".")
		if len(parts) > 0 {
			nodeID = parts[0]
		}
	}

	// Determine advertise addresses - use vpsIP if provided
	rqliteHTTP := strconv.Itoa(constants.RQLiteHTTPPort)
	raftPort := strconv.Itoa(constants.RQLiteRaftPort)
	var httpAdvAddr, raftAdvAddr string
	if vpsIP != "" {
		httpAdvAddr = net.JoinHostPort(vpsIP, rqliteHTTP)
		raftAdvAddr = net.JoinHostPort(vpsIP, raftPort)
	} else {
		httpAdvAddr = net.JoinHostPort("localhost", rqliteHTTP)
		raftAdvAddr = net.JoinHostPort("localhost", raftPort)
	}

	joinPort := raftPort

	var rqliteJoinAddr string
	if joinAddress != "" {
		if strings.Contains(joinAddress, ":7002") || strings.Contains(joinAddress, ":7001") {
			rqliteJoinAddr = strings.Replace(strings.Replace(joinAddress, ":7002", ":"+raftPort, 1), ":7001", ":"+raftPort, 1)
		} else {
			rqliteJoinAddr = joinAddress
		}
	} else if len(peerAddresses) > 0 {
		// Infer join address from peers
		peerIP := inferPeerIP(peerAddresses, "")
		if peerIP != "" {
			rqliteJoinAddr = net.JoinHostPort(peerIP, joinPort)
			// Validate that join address doesn't match this node's own raft address (would cause self-join)
			if rqliteJoinAddr == raftAdvAddr {
				rqliteJoinAddr = "" // Clear it - this is the first node
			}
		}
	}
	// If no join address and no peers, this is the first node - it will create the cluster

	// TLS/ACME configuration
	tlsCacheDir := ""
	httpPort := 80
	httpsPort := 443
	if enableHTTPS {
		tlsCacheDir = filepath.Join(cg.oramaDir, "tls-cache")
	}

	data := templates.NodeConfigData{
		NodeID:                 nodeID,
		P2PPort:                4001,
		DataDir:                filepath.Join(cg.oramaDir, "data"),
		RQLiteHTTPPort:         constants.RQLiteHTTPPort,
		RQLiteRaftPort:         constants.RQLiteRaftPort,
		RQLiteRaftInternalPort: constants.RQLiteRaftPort,
		RQLiteJoinAddress:      rqliteJoinAddr,
		BootstrapPeers:         peerAddresses,
		ClusterAPIPort:         constants.IPFSClusterAPIPort,
		IPFSAPIPort:            constants.IPFSAPIPort,
		OlricHTTPPort:          constants.OlricHTTPPort,
		HTTPAdvAddress:         httpAdvAddr,
		RaftAdvAddress:         raftAdvAddr,
		UnifiedGatewayPort:     constants.GatewayAPIPort,
		Domain:                 domain,
		BaseDomain:             baseDomain,
		EnableHTTPS:            enableHTTPS,
		TLSCacheDir:            tlsCacheDir,
		HTTPPort:               httpPort,
		HTTPSPort:              httpsPort,
		WGIP:                   vpsIP,
	}

	// MinClusterSize=1 for all nodes. Joining nodes use the -join flag to
	// connect to the existing cluster; gating on peer discovery caused a
	// deadlock where the WG sync loop (needs RQLite) couldn't add new peers
	// and RQLite (needs WG peers discovered) couldn't start.
	// Solo-bootstrap protection is already handled by performPreStartClusterDiscovery
	// which refuses to write a single-node peers.json.
	data.MinClusterSize = 1

	// RQLite node-to-node TLS encryption is disabled by default
	// This simplifies certificate management - RQLite uses plain TCP for internal Raft
	// HTTPS is still used for client-facing gateway traffic via autocert
	// TLS can be enabled manually later if needed for inter-node encryption

	// Operator metadata (set by orama node setup via --ssh-user, --environment, --operator-wallet)
	data.SSHUser = cg.SSHUser
	data.Environment = cg.Environment
	data.OperatorWallet = cg.OperatorWallet

	// Serverless function secrets encryption key (bugboard #837). Read the
	// persisted key (generated in Phase3 / received via join) so it is
	// rendered into node.yaml under http_gateway. If the file is missing the
	// key is left empty and omitted from the rendered config — get_secret then
	// stays disabled until the operator provisions the key. We deliberately do
	// NOT generate here: generation/distribution is owned by SecretGenerator
	// and the join flow so every node in a cluster shares one key.
	secretsKeyPath := filepath.Join(cg.oramaDir, "secrets", "secrets-encryption-key")
	if keyBytes, err := os.ReadFile(secretsKeyPath); err == nil {
		data.SecretsEncryptionKey = strings.TrimSpace(string(keyBytes))
	}

	// Shared self-hosted ntfy base URL (bugboard #858). Derive it the SAME way
	// the orchestrator derives the ntfy server + Caddy reverse-proxy host
	// (push.<dnsZone>, dnsZone = baseDomain or the node domain), so the gateway's
	// NtfyBaseURL matches and the push provider fans each publish out to every
	// active push node instead of single-host delivery. Without this the fan-out
	// code is inert and ~87% of publishes never reach a pinned subscriber.
	if dnsZone := baseDomain; dnsZone != "" {
		data.NtfyBaseURL = "https://push." + dnsZone
	} else if domain != "" {
		data.NtfyBaseURL = "https://push." + domain
	}

	// WebRTC/TURN config (feat-124 #913). The TURN secret lives in the secrets
	// dir so it survives Phase4 config regeneration; turn_domain/sfu_port/enabled
	// are operator-set values that only exist in the previous node.yaml, so we
	// carry them forward from the existing on-disk config. Without this, a regen
	// wipes the operator's manually-added webrtc block and the namespace
	// reconciler restarts gateways with an empty TURN secret (the outage).
	if err := cg.populateWebRTCConfig(&data); err != nil {
		return "", fmt.Errorf("failed to populate webrtc config: %w", err)
	}

	// Stealth TURN SNI router (feat-124). Like the webrtc block, sni_router is
	// an operator opt-in that only exists in the previous node.yaml, so carry
	// it forward across regeneration. Without this, a Phase4 regen would reset
	// sni_router.enabled to false, stop the :443 router and break stealth TURN
	// for every region that relies on it (the same regen-wipe class of outage
	// as bugboard #259/#846).
	cg.populateSNIRouterConfig(&data)

	return templates.RenderNodeConfig(data)
}

// populateSNIRouterConfig carries forward the operator-set sni_router.enabled
// flag from the existing node.yaml so a config regeneration never silently
// disables the stealth TURN-over-443 router. Absence of the file or block
// leaves the flag at its default (false).
func (cg *ConfigGenerator) populateSNIRouterConfig(data *templates.NodeConfigData) {
	data.SNIRouterEnabled = cg.readExistingSNIRouterEnabled()
}

// SNIRouterEnabled reports whether the node's on-disk node.yaml has opted in to
// the stealth TURN-over-443 SNI router. The orchestrator reads this AFTER
// Phase4 has written node.yaml to decide whether to move Caddy to :8443 and
// start the router unit. Returns false when the config or block is absent.
func (cg *ConfigGenerator) SNIRouterEnabled() bool {
	return cg.readExistingSNIRouterEnabled()
}

// readExistingSNIRouterEnabled parses just the top-level sni_router.enabled
// flag out of the existing node.yaml. Returns false when the file is missing,
// malformed, or has no sni_router block (fresh install / not opted in).
func (cg *ConfigGenerator) readExistingSNIRouterEnabled() bool {
	configPath := filepath.Join(cg.oramaDir, "configs", "node.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return false // No existing config (fresh install) — default off.
	}

	var parsed struct {
		SNIRouter struct {
			Enabled bool `yaml:"enabled"`
		} `yaml:"sni_router"`
	}
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return false // Malformed/old config — don't fail regen; default off.
	}
	return parsed.SNIRouter.Enabled
}

// existingWebRTC is the minimal shape parsed out of an existing node.yaml to
// carry forward operator-set WebRTC fields across a config regeneration.
type existingWebRTC struct {
	Enabled    bool
	SFUPort    int
	TURNDomain string
	TURNSecret string
}

// populateWebRTCConfig fills the WebRTC fields on data so the rendered node.yaml
// preserves operator TURN configuration across regenerations.
//
// Sources, in order of authority:
//   - turn_secret: the persisted secrets/turn-secret file (durable, survives
//     regen). If absent but the existing node.yaml carried a secret, that secret
//     is persisted to the file so it becomes durable from now on.
//   - turn_domain / sfu_port / enabled: carried forward from the existing
//     node.yaml's http_gateway.webrtc block (operator-set, not in secrets).
//
// If there is no persisted secret and no existing webrtc block, WebRTC is left
// disabled and the template renders nothing.
func (cg *ConfigGenerator) populateWebRTCConfig(data *templates.NodeConfigData) error {
	existing := cg.readExistingWebRTC()

	// Resolve the TURN secret: persisted file wins; otherwise adopt the secret
	// from the existing node.yaml and persist it so it is durable.
	secret := ""
	secretPath := filepath.Join(cg.oramaDir, "secrets", "turn-secret")
	if b, err := os.ReadFile(secretPath); err == nil {
		secret = strings.TrimSpace(string(b))
	}
	if secret == "" && existing != nil && existing.TURNSecret != "" {
		secret = existing.TURNSecret
		if err := cg.persistTURNSecret(secret); err != nil {
			return err
		}
	}

	if secret == "" {
		// No durable secret and nothing to adopt — leave WebRTC disabled.
		return nil
	}

	data.TURNSecret = secret
	data.WebRTCEnabled = true

	if existing != nil {
		data.TURNDomain = existing.TURNDomain
		data.SFUPort = existing.SFUPort
	}
	if data.SFUPort == 0 {
		data.SFUPort = defaultSFUSignalingPort
	}

	return nil
}

// readExistingWebRTC parses just the http_gateway.webrtc block out of the
// existing node.yaml. Absence of the file or block is tolerated (returns nil).
func (cg *ConfigGenerator) readExistingWebRTC() *existingWebRTC {
	configPath := filepath.Join(cg.oramaDir, "configs", "node.yaml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil // No existing config (fresh install) — nothing to carry forward.
	}

	var parsed struct {
		HTTPGateway struct {
			WebRTC struct {
				Enabled    bool   `yaml:"enabled"`
				SFUPort    int    `yaml:"sfu_port"`
				TURNDomain string `yaml:"turn_domain"`
				TURNSecret string `yaml:"turn_secret"`
			} `yaml:"webrtc"`
		} `yaml:"http_gateway"`
	}
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return nil // Malformed/old config — don't fail regen; just nothing to carry.
	}

	wb := parsed.HTTPGateway.WebRTC
	if !wb.Enabled && wb.SFUPort == 0 && wb.TURNDomain == "" && wb.TURNSecret == "" {
		return nil // No webrtc block present.
	}
	return &existingWebRTC{
		Enabled:    wb.Enabled,
		SFUPort:    wb.SFUPort,
		TURNDomain: wb.TURNDomain,
		TURNSecret: wb.TURNSecret,
	}
}

// persistTURNSecret writes the TURN secret to the secrets dir with 0600 perms
// and correct ownership, making it durable across future config regenerations.
func (cg *ConfigGenerator) persistTURNSecret(secret string) error {
	secretPath := filepath.Join(cg.oramaDir, "secrets", "turn-secret")
	secretDir := filepath.Dir(secretPath)
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return fmt.Errorf("failed to create secrets directory: %w", err)
	}
	if err := os.Chmod(secretDir, 0700); err != nil {
		return fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}
	if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
		return fmt.Errorf("failed to persist TURN secret: %w", err)
	}
	if err := ensureSecretFilePermissions(secretPath); err != nil {
		return err
	}
	return nil
}

// GenerateVaultConfig generates vault.yaml configuration for the Vault Guardian.
// The vault config uses key=value format (not YAML, despite the file extension).
// Peer discovery is dynamic via RQLite — no static peer list needed.
func (cg *ConfigGenerator) GenerateVaultConfig(vpsIP string) string {
	dataDir := filepath.Join(cg.oramaDir, "data", "vault")

	// Bind to WireGuard IP so vault is only accessible over the overlay network.
	// If no WG IP is provided, bind to localhost as a safe default.
	bindAddr := "127.0.0.1"
	if vpsIP != "" {
		bindAddr = vpsIP
	}

	return fmt.Sprintf(`# Vault Guardian Configuration
# Generated by orama node install

listen_address = %s
client_port = %d
peer_port = 7501
data_dir = %s
rqlite_url = http://127.0.0.1:%d
`, bindAddr, constants.VaultHTTPPort, dataDir, constants.RQLiteHTTPPort)
}

// GenerateGatewayConfig generates gateway.yaml configuration
func (cg *ConfigGenerator) GenerateGatewayConfig(peerAddresses []string, enableHTTPS bool, domain string, olricServers []string) (string, error) {
	tlsCacheDir := ""
	if enableHTTPS {
		tlsCacheDir = filepath.Join(cg.oramaDir, "tls-cache")
	}

	data := templates.GatewayConfigData{
		ListenPort:     constants.GatewayAPIPort,
		BootstrapPeers: peerAddresses,
		OlricServers:   olricServers,
		ClusterAPIPort: constants.IPFSClusterAPIPort,
		IPFSAPIPort:    constants.IPFSAPIPort,
		EnableHTTPS:    enableHTTPS,
		DomainName:     domain,
		TLSCacheDir:    tlsCacheDir,
		RQLiteDSN:      "", // Empty for now, can be configured later
	}
	return templates.RenderGatewayConfig(data)
}

// GenerateOlricConfig generates Olric configuration.
// Reads the Olric encryption key from secrets if available.
func (cg *ConfigGenerator) GenerateOlricConfig(serverBindAddr string, httpPort int, memberlistBindAddr string, memberlistPort int, memberlistEnv string, advertiseAddr string, peers []string) (string, error) {
	// Read encryption key from secrets if available
	encryptionKey := ""
	if data, err := os.ReadFile(filepath.Join(cg.oramaDir, "secrets", "olric-encryption-key")); err == nil {
		encryptionKey = strings.TrimSpace(string(data))
	}

	data := templates.OlricConfigData{
		ServerBindAddr:          serverBindAddr,
		HTTPPort:                httpPort,
		MemberlistBindAddr:      memberlistBindAddr,
		MemberlistPort:          memberlistPort,
		MemberlistEnvironment:   memberlistEnv,
		MemberlistAdvertiseAddr: advertiseAddr,
		Peers:                   peers,
		EncryptionKey:           encryptionKey,
	}
	return templates.RenderOlricConfig(data)
}

// SecretGenerator manages generation of shared secrets and keys
type SecretGenerator struct {
	oramaDir string
}

// NewSecretGenerator creates a new secret generator
func NewSecretGenerator(oramaDir string) *SecretGenerator {
	return &SecretGenerator{
		oramaDir: oramaDir,
	}
}

// ValidateClusterSecret ensures a cluster secret is 32 bytes of hex
func ValidateClusterSecret(secret string) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("cluster secret cannot be empty")
	}
	if len(secret) != 64 {
		return fmt.Errorf("cluster secret must be 64 hex characters (32 bytes)")
	}
	if _, err := hex.DecodeString(secret); err != nil {
		return fmt.Errorf("cluster secret must be valid hex: %w", err)
	}
	return nil
}

// EnsureClusterSecret gets or generates the IPFS Cluster secret
func (sg *SecretGenerator) EnsureClusterSecret() (string, error) {
	secretPath := filepath.Join(sg.oramaDir, "secrets", "cluster-secret")
	secretDir := filepath.Dir(secretPath)

	// Ensure secrets directory exists with restricted permissions (0700)
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create secrets directory: %w", err)
	}
	// Ensure directory permissions are correct even if it already existed
	if err := os.Chmod(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Try to read existing secret
	if data, err := os.ReadFile(secretPath); err == nil {
		secret := strings.TrimSpace(string(data))
		if len(secret) == 64 {
			if err := ensureSecretFilePermissions(secretPath); err != nil {
				return "", err
			}
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
	if err := ensureSecretFilePermissions(secretPath); err != nil {
		return "", err
	}

	return secret, nil
}

// EnsureRQLiteAuth generates the RQLite auth credentials and JSON auth file.
// Returns (username, password). The auth JSON file is written to secrets/rqlite-auth.json.
func (sg *SecretGenerator) EnsureRQLiteAuth() (string, string, error) {
	passwordPath := filepath.Join(sg.oramaDir, "secrets", "rqlite-password")
	authFilePath := filepath.Join(sg.oramaDir, "secrets", "rqlite-auth.json")
	secretDir := filepath.Dir(passwordPath)
	username := "orama"

	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return "", "", fmt.Errorf("failed to create secrets directory: %w", err)
	}
	if err := os.Chmod(secretDir, 0700); err != nil {
		return "", "", fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Try to read existing password
	var password string
	if data, err := os.ReadFile(passwordPath); err == nil {
		password = strings.TrimSpace(string(data))
	}

	// Generate new password if needed
	if password == "" {
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return "", "", fmt.Errorf("failed to generate RQLite password: %w", err)
		}
		password = hex.EncodeToString(bytes)

		if err := os.WriteFile(passwordPath, []byte(password), 0600); err != nil {
			return "", "", fmt.Errorf("failed to save RQLite password: %w", err)
		}
		if err := ensureSecretFilePermissions(passwordPath); err != nil {
			return "", "", err
		}
	}

	// Always regenerate the auth JSON file to ensure consistency
	authJSON := fmt.Sprintf(`[{"username": "%s", "password": "%s", "perms": ["all"]}]`, username, password)
	if err := os.WriteFile(authFilePath, []byte(authJSON), 0600); err != nil {
		return "", "", fmt.Errorf("failed to save RQLite auth file: %w", err)
	}
	if err := ensureSecretFilePermissions(authFilePath); err != nil {
		return "", "", err
	}

	return username, password, nil
}

// EnsureOlricEncryptionKey gets or generates a 32-byte encryption key for Olric memberlist gossip.
// The key is stored as base64 on disk and returned as base64 (what Olric expects).
func (sg *SecretGenerator) EnsureOlricEncryptionKey() (string, error) {
	secretPath := filepath.Join(sg.oramaDir, "secrets", "olric-encryption-key")
	secretDir := filepath.Dir(secretPath)

	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create secrets directory: %w", err)
	}
	if err := os.Chmod(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Try to read existing key
	if data, err := os.ReadFile(secretPath); err == nil {
		key := strings.TrimSpace(string(data))
		if key != "" {
			if err := ensureSecretFilePermissions(secretPath); err != nil {
				return "", err
			}
			return key, nil
		}
	}

	// Generate new 32-byte key, base64 encoded
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", fmt.Errorf("failed to generate Olric encryption key: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	if err := os.WriteFile(secretPath, []byte(key), 0600); err != nil {
		return "", fmt.Errorf("failed to save Olric encryption key: %w", err)
	}
	if err := ensureSecretFilePermissions(secretPath); err != nil {
		return "", err
	}

	return key, nil
}

// EnsureAPIKeyHMACSecret gets or generates the HMAC secret used to hash API keys.
// The secret is a 32-byte random value stored as 64 hex characters.
func (sg *SecretGenerator) EnsureAPIKeyHMACSecret() (string, error) {
	secretPath := filepath.Join(sg.oramaDir, "secrets", "api-key-hmac-secret")
	secretDir := filepath.Dir(secretPath)

	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create secrets directory: %w", err)
	}
	if err := os.Chmod(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Try to read existing secret
	if data, err := os.ReadFile(secretPath); err == nil {
		secret := strings.TrimSpace(string(data))
		if len(secret) == 64 {
			if err := ensureSecretFilePermissions(secretPath); err != nil {
				return "", err
			}
			return secret, nil
		}
	}

	// Generate new secret (32 bytes = 64 hex chars)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate API key HMAC secret: %w", err)
	}
	secret := hex.EncodeToString(bytes)

	if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
		return "", fmt.Errorf("failed to save API key HMAC secret: %w", err)
	}
	if err := ensureSecretFilePermissions(secretPath); err != nil {
		return "", err
	}

	return secret, nil
}

// EnsureSecretsEncryptionKey gets or generates the AES-256 key used to
// encrypt serverless function secrets at rest (the function_secrets table).
// The key is a 32-byte random value stored as 64 hex characters.
//
// It MUST be identical on every namespace-gateway node in a cluster and
// stable across restarts — otherwise secrets encrypted by one process can't
// be decrypted by another (bugboard #837). Like api-key-hmac-secret, joining
// nodes receive this value through the join flow rather than generating their
// own; this method only generates on the genesis node (or returns the
// existing key if a joining node already wrote it to disk).
func (sg *SecretGenerator) EnsureSecretsEncryptionKey() (string, error) {
	secretPath := filepath.Join(sg.oramaDir, "secrets", "secrets-encryption-key")
	secretDir := filepath.Dir(secretPath)

	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create secrets directory: %w", err)
	}
	if err := os.Chmod(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Try to read existing key
	if data, err := os.ReadFile(secretPath); err == nil {
		key := strings.TrimSpace(string(data))
		if len(key) == 64 {
			if err := ensureSecretFilePermissions(secretPath); err != nil {
				return "", err
			}
			return key, nil
		}
	}

	// Generate new key (32 bytes = 64 hex chars)
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", fmt.Errorf("failed to generate secrets encryption key: %w", err)
	}
	key := hex.EncodeToString(keyBytes)

	if err := os.WriteFile(secretPath, []byte(key), 0600); err != nil {
		return "", fmt.Errorf("failed to save secrets encryption key: %w", err)
	}
	if err := ensureSecretFilePermissions(secretPath); err != nil {
		return "", err
	}

	return key, nil
}

// EnsureTURNSecret gets or generates the HMAC-SHA1 shared secret used to mint
// TURN credentials for WebRTC (the http_gateway.webrtc.turn_secret field).
// The secret is a 32-byte random value stored as 64 hex characters.
//
// It MUST be identical on every namespace-gateway node in a cluster and stable
// across restarts AND config regenerations — otherwise the namespace reconciler
// sees drift (desired vs on-disk) and restarts gateways with an empty secret,
// which makes turn.credentials return namespace_not_configured (feat-124 #913,
// the AnChat outage). Persisting the secret to the secrets dir is what lets it
// survive Phase4 config regeneration: GenerateNodeConfig reads this file rather
// than relying on the (regenerated-from-template) node.yaml. Joining nodes
// receive the value through the join flow rather than generating their own.
func (sg *SecretGenerator) EnsureTURNSecret() (string, error) {
	secretPath := filepath.Join(sg.oramaDir, "secrets", "turn-secret")
	secretDir := filepath.Dir(secretPath)

	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create secrets directory: %w", err)
	}
	if err := os.Chmod(secretDir, 0700); err != nil {
		return "", fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Try to read existing secret
	if data, err := os.ReadFile(secretPath); err == nil {
		secret := strings.TrimSpace(string(data))
		if len(secret) == 64 {
			if err := ensureSecretFilePermissions(secretPath); err != nil {
				return "", err
			}
			return secret, nil
		}
	}

	// Generate new secret (32 bytes = 64 hex chars)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return "", fmt.Errorf("failed to generate TURN secret: %w", err)
	}
	secret := hex.EncodeToString(secretBytes)

	if err := os.WriteFile(secretPath, []byte(secret), 0600); err != nil {
		return "", fmt.Errorf("failed to save TURN secret: %w", err)
	}
	if err := ensureSecretFilePermissions(secretPath); err != nil {
		return "", err
	}

	return secret, nil
}

func ensureSecretFilePermissions(secretPath string) error {
	if err := os.Chmod(secretPath, 0600); err != nil {
		return fmt.Errorf("failed to set permissions on %s: %w", secretPath, err)
	}

	if usr, err := user.Lookup("orama"); err == nil {
		uid, err := strconv.Atoi(usr.Uid)
		if err != nil {
			return fmt.Errorf("failed to parse orama UID: %w", err)
		}
		gid, err := strconv.Atoi(usr.Gid)
		if err != nil {
			return fmt.Errorf("failed to parse orama GID: %w", err)
		}
		if err := os.Chown(secretPath, uid, gid); err != nil {
			return fmt.Errorf("failed to change ownership of %s: %w", secretPath, err)
		}
	}

	return nil
}

// EnsureSwarmKey gets or generates the IPFS private swarm key
func (sg *SecretGenerator) EnsureSwarmKey() ([]byte, error) {
	swarmKeyPath := filepath.Join(sg.oramaDir, "secrets", "swarm.key")
	secretDir := filepath.Dir(swarmKeyPath)

	// Ensure secrets directory exists with restricted permissions (0700)
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create secrets directory: %w", err)
	}
	// Ensure directory permissions are correct even if it already existed
	if err := os.Chmod(secretDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Try to read existing key — validate and auto-fix if corrupted (e.g. double headers)
	if data, err := os.ReadFile(swarmKeyPath); err == nil {
		content := string(data)
		if strings.Contains(content, "/key/swarm/psk/1.0.0/") {
			// Extract hex and rebuild clean file
			lines := strings.Split(strings.TrimSpace(content), "\n")
			hexKey := ""
			for i := len(lines) - 1; i >= 0; i-- {
				line := strings.TrimSpace(lines[i])
				if line != "" && !strings.HasPrefix(line, "/") {
					hexKey = line
					break
				}
			}
			clean := fmt.Sprintf("/key/swarm/psk/1.0.0/\n/base16/\n%s\n", hexKey)
			if clean != content {
				_ = os.WriteFile(swarmKeyPath, []byte(clean), 0600)
			}
			return []byte(clean), nil
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

// EnsureNodeIdentity gets or generates the node's LibP2P identity (unified - no bootstrap/node distinction)
func (sg *SecretGenerator) EnsureNodeIdentity() (peer.ID, error) {
	// Unified data directory (no bootstrap/node distinction)
	keyDir := filepath.Join(sg.oramaDir, "data")
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
	var configDir string
	// gateway.yaml goes to data/ directory, other configs go to configs/
	if filename == "gateway.yaml" {
		configDir = filepath.Join(sg.oramaDir, "data")
	} else {
		configDir = filepath.Join(sg.oramaDir, "configs")
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, filename)
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write config %s: %w", filename, err)
	}

	return nil
}
