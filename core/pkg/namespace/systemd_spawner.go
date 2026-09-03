package namespace

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/sfu"
	"github.com/DeBrosOfficial/network/pkg/systemd"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// SystemdSpawner spawns namespace cluster processes using systemd services
type SystemdSpawner struct {
	systemdMgr    *systemd.Manager
	namespaceBase string
	// clusterSecretPath is the host's cluster-secret file path; written
	// into spawned namespace gateways' YAML so they can derive the
	// cluster-wide JWT signing key (bug #215). Empty string means the host
	// has no cluster secret available — namespace gateways will fall back
	// to per-node random keys and JWTs won't verify cross-node.
	clusterSecretPath string
	logger            *zap.Logger

	// caddyStorageDirOverride overrides the Caddy cert-storage dir used to
	// locate the `*.<base>` wildcard cert. Empty means the production default
	// (caddyServiceStorageDir). Only set in tests so the wildcard-preference
	// branch of resolveTURNSCert can be exercised without touching /var/lib.
	caddyStorageDirOverride string
}

// wildcardCertPaths returns the cert/key paths for the `*.<baseDomain>` wildcard
// in Caddy's storage, honoring caddyStorageDirOverride when set (tests).
func (s *SystemdSpawner) wildcardCertPaths(baseDomain string) (certPath, keyPath string) {
	if s.caddyStorageDirOverride != "" {
		name := "wildcard_." + baseDomain
		dir := filepath.Join(s.caddyStorageDirOverride, caddyACMECertDir, name)
		return filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key")
	}
	return caddyWildcardCertPaths(baseDomain)
}

// NewSystemdSpawner creates a new systemd-based spawner.
//
// clusterSecretPath should point to the host node's cluster-secret file
// (typically `<oramaDir>/secrets/cluster-secret`). It is written into each
// spawned namespace gateway's YAML config so the gateway can read it on
// startup. Pass "" only if no cluster secret exists on this host (legacy
// single-node test deployments).
func NewSystemdSpawner(namespaceBase, clusterSecretPath string, logger *zap.Logger) *SystemdSpawner {
	return &SystemdSpawner{
		systemdMgr:        systemd.NewManager(namespaceBase, logger),
		namespaceBase:     namespaceBase,
		clusterSecretPath: clusterSecretPath,
		logger:            logger.With(zap.String("component", "systemd-spawner")),
	}
}

// joinVerifyTimeout bounds the pre-join identity check.
const joinVerifyTimeout = 10 * time.Second

// verifyJoinTarget refuses to start an RQLite node whose join target belongs to a
// DIFFERENT namespace (bugboard #275).
//
// rqlited joins whatever answers at its -join address; nothing in the protocol
// asserts the cluster is the right one. When a port collision put another
// namespace's rqlited on the expected port, a namespace node joined that foreign
// raft group as a Voter and served its database — the two namespaces silently
// shared storage, and the victim's quorum changed underneath it.
//
// The target's /status reports the data directory it is serving, which is rooted
// at .../namespaces/<namespace>/rqlite/<nodeID>. That is an unforgeable statement
// of which namespace the cluster belongs to, so require it to match ours.
func (s *SystemdSpawner) verifyJoinTarget(ctx context.Context, namespace, verifyURL string) error {
	if strings.TrimSpace(verifyURL) == "" {
		return nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, joinVerifyTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(verifyURL, "/")+"/status", nil)
	if err != nil {
		return fmt.Errorf("verify join target for namespace %s: %w", namespace, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("verify join target %s for namespace %s: %w", verifyURL, namespace, err)
	}
	defer resp.Body.Close()

	var status struct {
		Store struct {
			Dir string `json:"dir"`
		} `json:"store"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return fmt.Errorf("verify join target %s for namespace %s: decode status: %w", verifyURL, namespace, err)
	}

	want := string(os.PathSeparator) + "namespaces" + string(os.PathSeparator) + namespace + string(os.PathSeparator)
	if !strings.Contains(status.Store.Dir, want) {
		return fmt.Errorf(
			"refusing to join RQLite at %s for namespace %s: it is serving %q, which belongs to a different namespace — "+
				"joining it would put this node in another tenant's raft group and expose their database",
			verifyURL, namespace, status.Store.Dir)
	}
	return nil
}

// portFreeWaitTimeout bounds how long ensurePortsFree waits for a port we are
// about to bind to be released. A restart stops the old unit first, and systemd
// returns before the socket is always fully closed, so a short wait absorbs that
// without masking a genuine conflict.
const portFreeWaitTimeout = 10 * time.Second

// ensurePortsFree fails loudly when a port this namespace is about to bind is
// held by something else (bugboard #276).
//
// The port allocator picks a block using only the namespace_port_allocations
// table, so it cannot see a process that holds the port without a matching row —
// an orphaned namespace, or any other listener. Previously the spawned service
// simply crash-looped ("bind: address already in use", restart counter climbing)
// while provisioning still reported the cluster ready, and on the one node where
// the ports happened to be free the collision escalated into joining a FOREIGN
// namespace's raft group (bugboard #275). Refusing to start, with the port named,
// turns a silent corruption into an operator-actionable error.
func (s *SystemdSpawner) ensurePortsFree(namespace string, ports map[string]int) error {
	deadline := time.Now().Add(portFreeWaitTimeout)
	for name, port := range ports {
		if port <= 0 {
			continue
		}
		for {
			if !portInUse(port) {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf(
					"cannot start %s for namespace %s: port %d is already in use by another process — "+
						"the allocation for this namespace conflicts with something already listening on this node; "+
						"check for an orphaned namespace holding this port before retrying",
					name, namespace, port)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil
}

// portInUse reports whether anything is listening on the port locally.
func portInUse(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// SpawnRQLite starts a RQLite instance using systemd
func (s *SystemdSpawner) SpawnRQLite(ctx context.Context, namespace, nodeID string, cfg rqlite.InstanceConfig) error {
	s.logger.Info("Spawning RQLite via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Build join arguments
	joinArgs := ""
	if len(cfg.JoinAddresses) > 0 {
		joinArgs = fmt.Sprintf("-join %s", cfg.JoinAddresses[0])
		for _, addr := range cfg.JoinAddresses[1:] {
			joinArgs += fmt.Sprintf(",%s", addr)
		}
	}

	// Bugboard #281: a brand-new cluster must not inherit raft state left behind
	// by a previous namespace of the same name. Clearing here (rather than
	// trusting delete to have succeeded) is what makes re-creating a namespace
	// deterministic.
	if cfg.FreshStart {
		raftDir := filepath.Join(s.namespaceBase, namespace, "rqlite", nodeID)
		if _, statErr := os.Stat(raftDir); statErr == nil {
			s.logger.Warn("Clearing leftover RQLite state for a fresh namespace cluster (bugboard #281)",
				zap.String("namespace", namespace),
				zap.String("node_id", nodeID),
				zap.String("path", raftDir))
			if err := os.RemoveAll(raftDir); err != nil {
				return fmt.Errorf("failed to clear stale RQLite state at %s: %w", raftDir, err)
			}
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("failed to inspect RQLite state dir %s: %w", raftDir, statErr)
		}
	}

	if err := s.ensurePortsFree(namespace, map[string]int{
		"RQLite HTTP": cfg.HTTPPort,
		"RQLite Raft": cfg.RaftPort,
	}); err != nil {
		return err
	}

	if err := s.verifyJoinTarget(ctx, namespace, cfg.JoinVerifyURL); err != nil {
		return err
	}

	// Generate environment file
	envVars := map[string]string{
		"HTTP_ADDR":     fmt.Sprintf("0.0.0.0:%d", cfg.HTTPPort),
		"RAFT_ADDR":     fmt.Sprintf("0.0.0.0:%d", cfg.RaftPort),
		"HTTP_ADV_ADDR": cfg.HTTPAdvAddress,
		"RAFT_ADV_ADDR": cfg.RaftAdvAddress,
		"JOIN_ARGS":     joinArgs,
		"NODE_ID":       nodeID,
	}

	if err := s.systemdMgr.GenerateEnvFile(namespace, nodeID, systemd.ServiceTypeRQLite, envVars); err != nil {
		return fmt.Errorf("failed to generate RQLite env file: %w", err)
	}

	// Start the systemd service
	if err := s.systemdMgr.StartService(namespace, systemd.ServiceTypeRQLite); err != nil {
		return fmt.Errorf("failed to start RQLite service: %w", err)
	}

	// Wait for service to be active
	if err := s.waitForService(namespace, systemd.ServiceTypeRQLite, 30*time.Second); err != nil {
		return fmt.Errorf("RQLite service did not become active: %w", err)
	}

	s.logger.Info("RQLite spawned successfully via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return nil
}

// SpawnOlric starts an Olric instance using systemd
func (s *SystemdSpawner) SpawnOlric(ctx context.Context, namespace, nodeID string, cfg olric.InstanceConfig) error {
	s.logger.Info("Spawning Olric via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	if err := s.ensurePortsFree(namespace, map[string]int{
		"Olric HTTP":       cfg.HTTPPort,
		"Olric memberlist": cfg.MemberlistPort,
	}); err != nil {
		return err
	}

	// Validate BindAddr: 0.0.0.0 or empty causes IPv6 resolution on dual-stack hosts,
	// breaking memberlist UDP gossip over WireGuard. Resolve from wg0 as fallback.
	if cfg.BindAddr == "" || cfg.BindAddr == "0.0.0.0" {
		wgIP, err := getWireGuardIP()
		if err != nil {
			return fmt.Errorf("Olric BindAddr is %q and failed to detect WireGuard IP: %w", cfg.BindAddr, err)
		}
		s.logger.Warn("Olric BindAddr was invalid, resolved from wg0",
			zap.String("original", cfg.BindAddr),
			zap.String("resolved", wgIP),
			zap.String("namespace", namespace))
		cfg.BindAddr = wgIP
		if cfg.AdvertiseAddr == "" || cfg.AdvertiseAddr == "0.0.0.0" {
			cfg.AdvertiseAddr = wgIP
		}
	}

	// Create config directory
	configDir := filepath.Join(s.namespaceBase, namespace, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, fmt.Sprintf("olric-%s.yaml", nodeID))

	// Generate Olric YAML config
	type olricServerConfig struct {
		BindAddr string `yaml:"bindAddr"`
		BindPort int    `yaml:"bindPort"`
	}
	type olricMemberlistConfig struct {
		Environment string   `yaml:"environment"`
		BindAddr    string   `yaml:"bindAddr"`
		BindPort    int      `yaml:"bindPort"`
		Peers       []string `yaml:"peers,omitempty"`
	}
	type olricConfig struct {
		Server         olricServerConfig     `yaml:"server"`
		Memberlist     olricMemberlistConfig `yaml:"memberlist"`
		PartitionCount uint64                `yaml:"partitionCount"`
	}

	config := olricConfig{
		Server: olricServerConfig{
			BindAddr: cfg.BindAddr,
			BindPort: cfg.HTTPPort,
		},
		Memberlist: olricMemberlistConfig{
			Environment: "lan",
			BindAddr:    cfg.BindAddr,
			BindPort:    cfg.MemberlistPort,
			Peers:       cfg.PeerAddresses,
		},
		PartitionCount: 12, // Optimized for namespace clusters (vs 256 default)
	}

	configBytes, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal Olric config: %w", err)
	}

	if err := os.WriteFile(configPath, configBytes, 0644); err != nil {
		return fmt.Errorf("failed to write Olric config: %w", err)
	}

	s.logger.Info("Created Olric config file",
		zap.String("path", configPath),
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Generate environment file with Olric config path
	envVars := map[string]string{
		"OLRIC_SERVER_CONFIG": configPath,
	}

	if err := s.systemdMgr.GenerateEnvFile(namespace, nodeID, systemd.ServiceTypeOlric, envVars); err != nil {
		return fmt.Errorf("failed to generate Olric env file: %w", err)
	}

	// Start the systemd service
	if err := s.systemdMgr.StartService(namespace, systemd.ServiceTypeOlric); err != nil {
		return fmt.Errorf("failed to start Olric service: %w", err)
	}

	// Wait for service to be active
	if err := s.waitForService(namespace, systemd.ServiceTypeOlric, 30*time.Second); err != nil {
		return fmt.Errorf("Olric service did not become active: %w", err)
	}

	s.logger.Info("Olric spawned successfully via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return nil
}

// apiKeyHMACSecretFileName is the on-disk file name of the API-key HMAC
// secret under <oramaDir>/secrets/. Matches the path the main gateway
// reads in pkg/node/gateway.go, so namespace gateways hash API keys
// identically to it (bugboard #160 fix).
const apiKeyHMACSecretFileName = "api-key-hmac-secret"

// oramaDir returns the host's orama data directory (typically ~/.orama),
// derived from namespaceBase without hardcoding a production path. Every
// caller constructs namespaceBase as "<oramaDir>/data/namespaces" (see
// ClusterManagerConfig.BaseDataDir and pkg/node/gateway.go's baseDataDir),
// so two levels up recovers oramaDir — the same directory whose
// secrets/ subfolder the main gateway reads.
func (s *SystemdSpawner) oramaDir() string {
	return filepath.Join(s.namespaceBase, "..", "..")
}

// SpawnGateway starts a Gateway instance using systemd
func (s *SystemdSpawner) SpawnGateway(ctx context.Context, namespace, nodeID string, cfg gateway.InstanceConfig) error {
	s.logger.Info("Spawning Gateway via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	if err := s.ensurePortsFree(namespace, map[string]int{"Gateway HTTP": cfg.HTTPPort}); err != nil {
		return err
	}

	// Create config directory
	configDir := filepath.Join(s.namespaceBase, namespace, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, fmt.Sprintf("gateway-%s.yaml", nodeID))

	// Bugboard #160 fix: read the same API-key HMAC secret the main
	// gateway uses (pkg/node/gateway.go) so this namespace gateway hashes
	// keys identically. Without it, auth.Service.HashAPIKey returns keys
	// unchanged: the gateway can't authenticate any core-registry key
	// (stored as a 64-char HMAC-SHA256 hash) and would persist any key it
	// issues itself in plaintext. A namespace gateway with no secret can
	// never authenticate anything, so booting one without it is never the
	// right outcome — fail loud instead of silently degrading.
	apiKeyHMACSecretPath := filepath.Join(s.oramaDir(), "secrets", apiKeyHMACSecretFileName)
	secretBytes, err := os.ReadFile(apiKeyHMACSecretPath)
	if err != nil {
		return fmt.Errorf("read API-key HMAC secret at %s (required for namespace gateway auth): %w", apiKeyHMACSecretPath, err)
	}
	apiKeyHMACSecret := strings.TrimSpace(string(secretBytes))
	if apiKeyHMACSecret == "" {
		return fmt.Errorf("API-key HMAC secret file %s is empty; namespace gateway cannot authenticate without it", apiKeyHMACSecretPath)
	}

	// Build Gateway YAML config using the shared type from gateway package
	gatewayConfig := gateway.GatewayYAMLConfig{
		ListenAddr:            fmt.Sprintf(":%d", cfg.HTTPPort),
		ClientNamespace:       cfg.Namespace,
		RQLiteDSN:             cfg.RQLiteDSN,
		GlobalRQLiteDSN:       cfg.GlobalRQLiteDSN,
		DomainName:            cfg.BaseDomain,
		OlricServers:          cfg.OlricServers,
		OlricTimeout:          cfg.OlricTimeout.String(),
		IPFSClusterAPIURL:     cfg.IPFSClusterAPIURL,
		IPFSAPIURL:            cfg.IPFSAPIURL,
		IPFSTimeout:           cfg.IPFSTimeout.String(),
		IPFSReplicationFactor: cfg.IPFSReplicationFactor,
		// Bug #215 fix: forward the host's cluster secret path so the
		// spawned namespace gateway can derive the cluster-wide JWT
		// signing key. Without this, namespace gateways used per-node
		// random Ed25519 keys and host functions saw empty
		// caller_jwt_subject.
		ClusterSecretPath: s.clusterSecretPath,
		// Bugboard #837 follow-up: forward the host's serverless secrets
		// encryption key so the spawned namespace gateway can manage function
		// secrets. Without this, `function secrets list` returned 501 on
		// namespace gateways even though the host gateway had the key.
		SecretsEncryptionKey: cfg.SecretsEncryptionKey,
		// Bugboard #160 fix: forward the API-key HMAC secret read above so
		// this namespace gateway hashes/verifies API keys identically to
		// the main gateway.
		APIKeyHMACSecret: apiKeyHMACSecret,
		WebRTC: gateway.GatewayYAMLWebRTC{
			Enabled:           cfg.WebRTCEnabled,
			SFUPort:           cfg.SFUPort,
			TURNDomain:        cfg.TURNDomain,
			TURNSecret:        cfg.TURNSecret,
			TURNStealthDomain: cfg.TURNStealthDomain,
		},
	}

	configBytes, err := yaml.Marshal(gatewayConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal Gateway config: %w", err)
	}

	// 0600: the gateway YAML embeds the secrets encryption key (bugboard
	// #837), so it must not be world/group readable.
	if err := os.WriteFile(configPath, configBytes, 0600); err != nil {
		return fmt.Errorf("failed to write Gateway config: %w", err)
	}
	// WriteFile's mode only applies on CREATE — converge perms explicitly so
	// a file written 0644 by an older release doesn't stay world-readable
	// after an in-place rewrite.
	if err := os.Chmod(configPath, 0600); err != nil {
		return fmt.Errorf("failed to set Gateway config permissions: %w", err)
	}

	s.logger.Info("Created Gateway config file",
		zap.String("path", configPath),
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Generate environment file with Gateway config path
	envVars := map[string]string{
		"GATEWAY_CONFIG": configPath,
	}

	if err := s.systemdMgr.GenerateEnvFile(namespace, nodeID, systemd.ServiceTypeGateway, envVars); err != nil {
		return fmt.Errorf("failed to generate Gateway env file: %w", err)
	}

	// Start the systemd service
	if err := s.systemdMgr.StartService(namespace, systemd.ServiceTypeGateway); err != nil {
		return fmt.Errorf("failed to start Gateway service: %w", err)
	}

	// Wait for service to be active
	if err := s.waitForService(namespace, systemd.ServiceTypeGateway, 30*time.Second); err != nil {
		return fmt.Errorf("Gateway service did not become active: %w", err)
	}

	s.logger.Info("Gateway spawned successfully via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return nil
}

// StopRQLite stops a RQLite instance
func (s *SystemdSpawner) StopRQLite(ctx context.Context, namespace, nodeID string) error {
	s.logger.Info("Stopping RQLite via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return s.systemdMgr.StopService(namespace, systemd.ServiceTypeRQLite)
}

// StopOlric stops an Olric instance
func (s *SystemdSpawner) StopOlric(ctx context.Context, namespace, nodeID string) error {
	s.logger.Info("Stopping Olric via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return s.systemdMgr.StopService(namespace, systemd.ServiceTypeOlric)
}

// StopGateway stops a Gateway instance
func (s *SystemdSpawner) StopGateway(ctx context.Context, namespace, nodeID string) error {
	s.logger.Info("Stopping Gateway via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return s.systemdMgr.StopService(namespace, systemd.ServiceTypeGateway)
}

// RestartGateway stops and re-spawns a Gateway instance with updated config.
// Used when gateway config changes at runtime (e.g., WebRTC enable/disable).
func (s *SystemdSpawner) RestartGateway(ctx context.Context, namespace, nodeID string, cfg gateway.InstanceConfig) error {
	s.logger.Info("Restarting Gateway via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Stop existing service (ignore error if already stopped)
	if err := s.systemdMgr.StopService(namespace, systemd.ServiceTypeGateway); err != nil {
		s.logger.Warn("Failed to stop Gateway before restart (may not be running)",
			zap.String("namespace", namespace),
			zap.Error(err))
	}

	// Re-spawn with updated config
	return s.SpawnGateway(ctx, namespace, nodeID, cfg)
}

// gatewayWebRTCInSync reports whether the WebRTC block already on disk
// matches the desired gateway config — i.e. no restart is needed.
// Compares only the WebRTC-relevant fields (bugboard #25 drift surface).
// Pure function so the reconcile decision is unit-testable without files
// or systemd.
func gatewayWebRTCInSync(onDisk gateway.GatewayYAMLWebRTC, cfg gateway.InstanceConfig) bool {
	return onDisk.Enabled == cfg.WebRTCEnabled &&
		onDisk.SFUPort == cfg.SFUPort &&
		onDisk.TURNSecret == cfg.TURNSecret &&
		onDisk.TURNDomain == cfg.TURNDomain &&
		onDisk.TURNStealthDomain == cfg.TURNStealthDomain
}

// gatewayConfigInSync reports whether the full reconcile-relevant config on
// disk matches the desired config — i.e. no rewrite+restart is needed.
// Combines the WebRTC drift surface (bugboard #25) with the secrets
// encryption key (bugboard #837): a gateway that was spawned before the key
// was plumbed has an empty on-disk key and `function secrets list` returns
// 501; once the desired key is non-empty we want a rewrite+restart so the
// running gateway picks it up.
//
// Plain string equality keeps the "both empty → in sync" case a no-op: a
// namespace on a host with no secrets key (empty desired) whose on-disk key
// is also empty is in-sync, so it never restart-loops. Only a genuine
// difference (empty on-disk vs non-empty desired, or a rotated key) drifts.
func gatewayConfigInSync(onDisk gateway.GatewayYAMLConfig, cfg gateway.InstanceConfig) bool {
	return gatewayWebRTCInSync(onDisk.WebRTC, cfg) &&
		onDisk.SecretsEncryptionKey == cfg.SecretsEncryptionKey
}

// ReconcileGateway is the WARM counterpart to SpawnGateway: when a
// namespace gateway is already running, this compares its on-disk config
// against the desired `cfg` and restarts it ONLY if the WebRTC block has
// drifted (enabled / sfu_port / turn_secret / turn_domain differ).
//
// Bugboard #25: the from-disk restore skips healthy gateways, so a
// gateway that lost its webrtc block on a prior restart (while staying
// healthy) never gets its config regenerated — leaving SFU/TURN services
// running but the gateway with no turn_secret/sfu_port (credentials
// configured:false, /v1/webrtc/turn/credentials 404). The cold-spawn
// self-heal only fires when the gateway happens to be down during
// restore. This closes that gap for the healthy case.
//
// Idempotent: returns nil WITHOUT restarting when the on-disk WebRTC
// block already matches the desired config — so it does not cause a
// restart loop on every node boot. WebRTC is the only known config-drift
// surface (bugboard #25); other fields are intentionally not compared to
// avoid spurious restarts from harmless differences (e.g. olric server
// ordering).
func (s *SystemdSpawner) ReconcileGateway(ctx context.Context, namespace, nodeID string, cfg gateway.InstanceConfig) error {
	configPath := filepath.Join(s.namespaceBase, namespace, "configs", fmt.Sprintf("gateway-%s.yaml", nodeID))
	existing, err := os.ReadFile(configPath)
	if err != nil {
		// No readable config to compare against — don't blindly restart a
		// healthy gateway; absence of the config file is a different
		// problem the caller's cold-spawn path handles.
		return fmt.Errorf("read gateway config for reconcile: %w", err)
	}
	var onDisk gateway.GatewayYAMLConfig
	if err := yaml.Unmarshal(existing, &onDisk); err != nil {
		return fmt.Errorf("parse gateway config for reconcile: %w", err)
	}

	if gatewayConfigInSync(onDisk, cfg) {
		// Already in sync — nothing to do, no restart.
		return nil
	}

	// secretsKeyDrifted is logged (as a bool, never the key material) so
	// operators can see when a #837 rewrite fires vs a #25 WebRTC rewrite.
	secretsKeyDrifted := onDisk.SecretsEncryptionKey != cfg.SecretsEncryptionKey
	s.logger.Info("Gateway config drifted from desired; reconciling (rewrite + restart)",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID),
		zap.Bool("ondisk_enabled", onDisk.WebRTC.Enabled),
		zap.Int("ondisk_sfu_port", onDisk.WebRTC.SFUPort),
		zap.Bool("desired_enabled", cfg.WebRTCEnabled),
		zap.Int("desired_sfu_port", cfg.SFUPort),
		zap.Bool("secrets_key_drifted", secretsKeyDrifted))
	return s.RestartGateway(ctx, namespace, nodeID, cfg)
}


// turnSecretDrift reports whether the on-disk TURN auth_secret differs from the
// desired (current DB) secret — i.e. a rewrite+restart is needed. Pure function
// so the reconcile decision is unit-testable.
func turnSecretDrift(onDiskSecret, dbSecret string) bool {
	return onDiskSecret != dbSecret
}



// SFUInstanceConfig holds configuration for spawning an SFU instance
type SFUInstanceConfig struct {
	Namespace      string
	NodeID         string
	ListenAddr     string                 // WireGuard IP:port (e.g., "10.0.0.1:30000")
	MediaPortStart int                    // Start of RTP media port range
	MediaPortEnd   int                    // End of RTP media port range
	TURNServers    []sfu.TURNServerConfig // TURN servers to advertise to peers
	TURNSecret     string                 // HMAC-SHA1 shared secret
	TURNCredTTL    int                    // Credential TTL in seconds
	RQLiteDSN      string                 // Namespace-local RQLite DSN
}

// SpawnSFU starts an SFU instance using systemd
func (s *SystemdSpawner) SpawnSFU(ctx context.Context, namespace, nodeID string, cfg SFUInstanceConfig) error {
	s.logger.Info("Spawning SFU via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID),
		zap.String("listen_addr", cfg.ListenAddr))

	// Create config directory
	configDir := filepath.Join(s.namespaceBase, namespace, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, fmt.Sprintf("sfu-%s.yaml", nodeID))

	// Build SFU YAML config
	sfuConfig := sfu.Config{
		ListenAddr:        cfg.ListenAddr,
		Namespace:         cfg.Namespace,
		MediaPortStart:    cfg.MediaPortStart,
		MediaPortEnd:      cfg.MediaPortEnd,
		TURNServers:       cfg.TURNServers,
		TURNSecret:        cfg.TURNSecret,
		TURNCredentialTTL: cfg.TURNCredTTL,
		RQLiteDSN:         cfg.RQLiteDSN,
	}

	configBytes, err := yaml.Marshal(sfuConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal SFU config: %w", err)
	}

	if err := writeConfigAtomic(configPath, configBytes, 0644); err != nil {
		return fmt.Errorf("failed to write SFU config: %w", err)
	}

	s.logger.Info("Created SFU config file",
		zap.String("path", configPath),
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Generate environment file pointing to config
	envVars := map[string]string{
		"SFU_CONFIG": configPath,
	}

	if err := s.systemdMgr.GenerateEnvFile(namespace, nodeID, systemd.ServiceTypeSFU, envVars); err != nil {
		return fmt.Errorf("failed to generate SFU env file: %w", err)
	}

	// Start the systemd service
	if err := s.systemdMgr.StartService(namespace, systemd.ServiceTypeSFU); err != nil {
		return fmt.Errorf("failed to start SFU service: %w", err)
	}

	// Wait for service to be active
	if err := s.waitForService(namespace, systemd.ServiceTypeSFU, 30*time.Second); err != nil {
		return fmt.Errorf("SFU service did not become active: %w", err)
	}

	s.logger.Info("SFU spawned successfully via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return nil
}

// StopSFU stops an SFU instance
func (s *SystemdSpawner) StopSFU(ctx context.Context, namespace, nodeID string) error {
	s.logger.Info("Stopping SFU via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return s.systemdMgr.StopService(namespace, systemd.ServiceTypeSFU)
}


// acmeInternalEndpoint is the gateway's internal ACME endpoint that the
// Caddyfile TURN-cert blocks point the orama DNS provider at.
const acmeInternalEndpoint = "http://localhost:6001/v1/internal/acme"

// turnCertProvisionTimeout bounds how long a TURN spawn waits for Caddy to
// provision a Let's Encrypt cert before falling back (primary domain) or
// failing (stealth domain).
const turnCertProvisionTimeout = 2 * time.Minute

// resolveTURNSCert resolves the TURNS cert/key pair for a domain.
//
// The Caddy `*.<baseDomain>` wildcard cert is preferred whenever baseDomain is
// set and the wildcard is on disk: the client-facing TURNS host is now a
// single-label subdomain (turn.TLSHostForNamespace), which the wildcard covers,
// so no per-namespace ACME provisioning is needed and the browser gets a
// CA-valid cert. This is the fix for TURNS being stuck on a self-signed cert —
// the legacy two-label host could never get a real cert (provisionTURNCertViaCaddy
// can't write /etc/caddy under ProtectSystem=strict, and the wildcard doesn't
// cover a two-label host).
//
// When no wildcard is available, per-domain Let's Encrypt via Caddy is tried
// next (idempotent, self-heals a node stuck on self-signed once Caddy can
// provision), then the self-signed fallback.
//
// allowSelfSigned controls the fallback: the primary TURN domain may fall
// back to (or reuse) a self-signed pair at <configDir>/turn-{cert,key}.pem so
// baseline TURN stays up, while the stealth domain must hard-fail instead.
func (s *SystemdSpawner) resolveTURNSCert(namespace, domain, baseDomain, publicIP, configDir string, allowSelfSigned bool) (string, string, error) {
	// Prefer the Caddy `*.<baseDomain>` wildcard cert. The client-facing TURNS
	// host is now a single-label subdomain (turn.TLSHostForNamespace), which the
	// wildcard covers — so the TURN server can present a CA-valid cert with no
	// per-namespace ACME provisioning, the same cert reuse that makes stealth
	// work. This is the fix for TURNS being stuck on a browser-rejected
	// self-signed cert: the legacy two-label host could never get a real cert
	// because provisionTURNCertViaCaddy can't write /etc/caddy under
	// ProtectSystem=strict, and the wildcard doesn't cover a two-label host.
	if baseDomain != "" {
		wcCert, wcKey := s.wildcardCertPaths(baseDomain)
		_, certErr := os.Stat(wcCert)
		_, keyErr := os.Stat(wcKey)
		if certErr == nil && keyErr == nil {
			s.logger.Info("Using Caddy wildcard cert for TURNS",
				zap.String("namespace", namespace),
				zap.String("base_domain", baseDomain),
				zap.String("cert_path", wcCert))
			return wcCert, wcKey, nil
		}
	}
	if domain != "" {
		caddyCert, caddyKey, err := provisionTURNCertViaCaddy(domain, acmeInternalEndpoint, turnCertProvisionTimeout)
		if err == nil {
			s.logger.Info("Using Let's Encrypt cert from Caddy for TURNS",
				zap.String("namespace", namespace),
				zap.String("domain", domain),
				zap.String("cert_path", caddyCert))
			return caddyCert, caddyKey, nil
		}
		if !allowSelfSigned {
			return "", "", fmt.Errorf("failed to provision Let's Encrypt cert for stealth TURNS domain %s (no self-signed fallback — clients must be able to validate it): %w", domain, err)
		}
		s.logger.Warn("Let's Encrypt cert provisioning failed, falling back to self-signed",
			zap.String("namespace", namespace),
			zap.String("domain", domain),
			zap.Error(err))
	}
	if !allowSelfSigned {
		return "", "", fmt.Errorf("no domain configured for TURNS cert in namespace %s", namespace)
	}

	certPath := filepath.Join(configDir, "turn-cert.pem")
	keyPath := filepath.Join(configDir, "turn-key.pem")
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		if err := turn.GenerateSelfSignedCert(certPath, keyPath, publicIP); err != nil {
			return "", "", fmt.Errorf("failed to generate TURNS self-signed cert for namespace %s: %w", namespace, err)
		}
		s.logger.Info("Generated TURNS self-signed certificate",
			zap.String("namespace", namespace),
			zap.String("cert_path", certPath))
	}
	return certPath, keyPath, nil
}

// resolveStealthCert resolves the TLS cert/key for the stealth TURNS host by
// reusing Caddy's existing `*.<baseDomain>` wildcard certificate (feat-124).
//
// The stealth host is a single-label subdomain of the base domain
// (cdn-<hash>.<baseDomain>), so the wildcard the gateway already provisions
// for HTTPS covers it. This deliberately avoids the runtime
// append-to-Caddyfile provisioning path: the orama-node service runs
// ProtectSystem=strict as the orama user and cannot write /etc/caddy, so that
// path fails with EROFS (and would silently fall back to a self-signed cert
// that clients reject — indistinguishable from being blocked). Caddy renews
// the wildcard; the TURN cert reloader hot-reloads it from storage.
//
// Hard error (never self-signed) when the wildcard is missing or the host is
// not a single-label subdomain — a stealth endpoint with an unvalidatable
// cert is worse than no stealth endpoint.
func (s *SystemdSpawner) resolveStealthCert(stealthDomain, baseDomain string) (string, string, error) {
	if baseDomain == "" {
		return "", "", fmt.Errorf("stealth cert: base domain required")
	}
	if !isSingleLabelSubdomain(stealthDomain, baseDomain) {
		return "", "", fmt.Errorf("stealth cert: %q is not a single-label subdomain of %q (the *.%s wildcard cert would not cover it)", stealthDomain, baseDomain, baseDomain)
	}
	certPath, keyPath := caddyWildcardCertPaths(baseDomain)
	if _, err := os.Stat(certPath); err != nil {
		return "", "", fmt.Errorf("stealth cert: Caddy wildcard cert for *.%s not found at %s (is the gateway HTTPS wildcard provisioned on this node?): %w", baseDomain, certPath, err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		return "", "", fmt.Errorf("stealth cert: Caddy wildcard key for *.%s not found at %s: %w", baseDomain, keyPath, err)
	}
	s.logger.Info("Using Caddy wildcard cert for stealth TURNS",
		zap.String("stealth_domain", stealthDomain),
		zap.String("cert_path", certPath))
	return certPath, keyPath, nil
}

// isSingleLabelSubdomain reports whether host is exactly one DNS label below
// base (e.g. "cdn-x.example.com" under "example.com"), which is the set a
// `*.base` wildcard certificate covers.
func isSingleLabelSubdomain(host, base string) bool {
	suffix := "." + base
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	label := strings.TrimSuffix(host, suffix)
	return label != "" && !strings.Contains(label, ".")
}


// StopTURN stops a TURN instance
func (s *SystemdSpawner) StopTURN(ctx context.Context, namespace, nodeID string) error {
	s.logger.Info("Stopping TURN via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	err := s.systemdMgr.StopService(namespace, systemd.ServiceTypeTURN)

	// The TURN relay ports are deliberately NOT closed here (bugboard #283 part
	// 2). 3478/5349 and the relay range are host-wide, shared by every namespace
	// on this node, so closing them because ONE namespace stopped would black out
	// TURN for all the others — recovering only on the next 60s reconcile, with
	// in-flight calls dying rather than reconnecting. Only a host that serves no
	// tenants at all should close them, which ReconcileHostTURN decides.
	//
	// Removing the per-namespace Caddyfile cert block is likewise gone: the realm
	// used to come from the per-namespace TURN config, which the shared server
	// replaced and stopLegacyPerNamespaceTURN deletes. The shared TURNS listener
	// uses the zone wildcard cert, so there is no per-namespace cert block to
	// retire.
	return err
}

// SaveClusterState writes cluster state JSON to the namespace data directory.
// Used by the spawn handler to persist state received from the coordinator node.
func (s *SystemdSpawner) SaveClusterState(namespace string, data []byte) error {
	dir := filepath.Join(s.namespaceBase, namespace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create namespace dir: %w", err)
	}
	path := filepath.Join(dir, "cluster-state.json")
	// Atomic write to a temp file + rename: cluster-state.json carries the
	// namespace TURN shared secret (bugboard #130), so it must not be
	// world/group readable on the receiving node either, and a reader must
	// never see a half-written secret. 0600 + chmod on the temp file keeps the
	// secret private; the rename then makes the live file 0600 too, tightening
	// a file an older release wrote 0644.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp cluster state: %w", err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("failed to set cluster state permissions: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("failed to rename cluster state into place: %w", err)
	}
	s.logger.Info("Saved cluster state from coordinator",
		zap.String("namespace", namespace),
		zap.String("path", path))
	return nil
}

// DeleteClusterState removes cluster state and config files for a namespace.
func (s *SystemdSpawner) DeleteClusterState(namespace string) error {
	dir := filepath.Join(s.namespaceBase, namespace)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete namespace data directory: %w", err)
	}
	s.logger.Info("Deleted namespace data directory",
		zap.String("namespace", namespace),
		zap.String("path", dir))
	return nil
}

// StopAll stops all services for a namespace, including deployment processes
func (s *SystemdSpawner) StopAll(ctx context.Context, namespace string) error {
	s.logger.Info("Stopping all namespace services via systemd",
		zap.String("namespace", namespace))

	// Stop deployment processes first (they depend on the cluster services)
	s.systemdMgr.StopDeploymentServicesForNamespace(namespace)

	// Then stop infrastructure services (Gateway → Olric → RQLite)
	return s.systemdMgr.StopAllNamespaceServices(namespace)
}

// waitForService waits for a systemd service to become active
func (s *SystemdSpawner) waitForService(namespace string, serviceType systemd.ServiceType, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		active, err := s.systemdMgr.IsServiceActive(namespace, serviceType)
		if err != nil {
			return fmt.Errorf("failed to check service status: %w", err)
		}

		if active {
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("service did not become active within %v", timeout)
}

// writeConfigAtomic writes a service config via temp-file + rename.
//
// Two writers can now target the same config concurrently — the boot restore and
// the 60s WebRTC reconciler (bugboard #161) — and a plain os.WriteFile truncates
// in place, so an interleaved write can leave truncated or malformed YAML on
// disk. The unit then fails to parse and crash-loops, which the reconciler
// retries forever. rename(2) is atomic within a filesystem, so a reader sees
// either the old file or the new one, never a half-written one.
func writeConfigAtomic(configPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(configPath)
	tmp, err := os.CreateTemp(dir, filepath.Base(configPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	// fsync before rename so a crash cannot leave the new name pointing at
	// unflushed (zero-length) content.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, configPath); err != nil {
		return fmt.Errorf("rename temp config into place: %w", err)
	}
	return nil
}
