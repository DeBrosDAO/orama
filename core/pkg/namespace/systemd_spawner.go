package namespace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	production "github.com/DeBrosOfficial/network/pkg/environments/production"
	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/secrets"
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

// SpawnGateway starts a Gateway instance using systemd
func (s *SystemdSpawner) SpawnGateway(ctx context.Context, namespace, nodeID string, cfg gateway.InstanceConfig) error {
	s.logger.Info("Spawning Gateway via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Create config directory
	configDir := filepath.Join(s.namespaceBase, namespace, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, fmt.Sprintf("gateway-%s.yaml", nodeID))

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

// TURNConfigExists reports whether this node has a TURN config for the
// namespace on disk — i.e. it is provisioned to run TURN. Deterministic (unlike
// checking the systemd unit state, which is racy during a restart), so callers
// can reliably decide whether to advertise this node in TURN DNS (bugboard #158).
func (s *SystemdSpawner) TURNConfigExists(namespace, nodeID string) bool {
	configPath := filepath.Join(s.namespaceBase, namespace, "configs", fmt.Sprintf("turn-%s.yaml", nodeID))
	_, err := os.Stat(configPath)
	return err == nil
}

// turnSecretDrift reports whether the on-disk TURN auth_secret differs from the
// desired (current DB) secret — i.e. a rewrite+restart is needed. Pure function
// so the reconcile decision is unit-testable.
func turnSecretDrift(onDiskSecret, dbSecret string) bool {
	return onDiskSecret != dbSecret
}

// reconcileTURNConfigFields applies desired state to a parsed TURN config in
// place and returns the names of the fields that actually changed (empty slice
// = no change, so the caller skips the rewrite+restart). Pure — the reconcile
// decision is unit-testable without systemd or disk.
//
// wildcardCert/wildcardKey are the `*.<base>` wildcard paths, or "" when the
// wildcard isn't available; the cert is switched only when both are non-empty,
// TURNS is enabled, and the config isn't already pointing at it.
func reconcileTURNConfigFields(cfg *turn.Config, dbSecret, wildcardCert, wildcardKey string) []string {
	var reconciled []string

	// (1) auth_secret drift. Never write an unresolved/encrypted secret (that
	// would make TURN validate with ciphertext and break every call) — parity
	// with the gateway's #130 skip.
	if dbSecret != "" && !secrets.IsEncrypted(dbSecret) && turnSecretDrift(cfg.AuthSecret, dbSecret) {
		cfg.AuthSecret = dbSecret
		reconciled = append(reconciled, "auth_secret")
	}

	// (2) TURNS cert drift. Switch the self-signed fallback to the wildcard so
	// the cert validates in browsers (self-signed is rejected).
	if cfg.TURNSListenAddr != "" && wildcardCert != "" && wildcardKey != "" && cfg.TLSCertPath != wildcardCert {
		cfg.TLSCertPath = wildcardCert
		cfg.TLSKeyPath = wildcardKey
		reconciled = append(reconciled, "tls_cert")
	}

	return reconciled
}

// ReconcileTURN keeps a running TURN server's on-disk config in sync with the
// current namespace state, restarting TURN only when something actually changed:
//   - auth_secret drift vs the DB secret (bugboard #158)
//   - the TURNS TLS cert still on the browser-rejected self-signed pair while
//     the `*.<base>` wildcard cert is now available on disk (TURNS cert fix)
//
// Both matter for the SAME reason: the TURN config is otherwise only (re)written
// on a COLD spawn (unit DOWN). A rolling deploy RESTARTS the unit, so it re-reads
// the stale file — a new binary's resolveTURNSCert / secret logic never runs. So
// on a warm restart the gateway would mint creds TURN rejects (Allocate 400 →
// zero candidates), and TURNS would keep presenting the self-signed cert browsers
// reject. This warm reconcile closes both gaps.
//
// SELF-CONTAINED: reads everything else (ports, public IP, stealth domain, base
// domain) from the on-disk turn config and patches ONLY the drifted fields — so
// it works even for namespaces whose cluster-state.json doesn't persist TURN
// fields. Idempotent: a no-op when there is no TURN config on this node or
// nothing drifted, so it never restart-loops or drops calls on an in-sync node.
func (s *SystemdSpawner) ReconcileTURN(ctx context.Context, namespace, nodeID, dbSecret string) error {
	configPath := filepath.Join(s.namespaceBase, namespace, "configs", fmt.Sprintf("turn-%s.yaml", nodeID))
	existing, err := os.ReadFile(configPath)
	if err != nil {
		// No TURN config on this node → this node doesn't run TURN for the
		// namespace. Nothing to reconcile.
		return nil
	}
	var cfg turn.Config
	if err := yaml.Unmarshal(existing, &cfg); err != nil {
		return fmt.Errorf("parse TURN config for reconcile: %w", err)
	}

	// Resolve the wildcard cert paths ONLY if TURNS is enabled and the wildcard
	// actually exists on disk — otherwise pass "" so the cert stays untouched (a
	// node without the wildcard keeps its self-signed baseline rather than losing
	// TURNS). The reconcile decision itself is a pure function (testable).
	var wcCert, wcKey string
	if cfg.TURNSListenAddr != "" && cfg.Realm != "" {
		c, k := s.wildcardCertPaths(cfg.Realm)
		if _, e1 := os.Stat(c); e1 == nil {
			if _, e2 := os.Stat(k); e2 == nil {
				wcCert, wcKey = c, k
			}
		}
	}

	reconciled := reconcileTURNConfigFields(&cfg, dbSecret, wcCert, wcKey)
	if len(reconciled) == 0 {
		return nil // already in sync — no restart
	}

	out, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshal TURN config for reconcile: %w", err)
	}
	if err := writeConfigAtomic(configPath, out, 0644); err != nil {
		return fmt.Errorf("write TURN config for reconcile: %w", err)
	}

	s.logger.Info("TURN config drifted from desired state — rewrote config and restarting TURN",
		zap.String("namespace", namespace), zap.String("node_id", nodeID),
		zap.Strings("reconciled", reconciled))
	return s.systemdMgr.RestartService(namespace, systemd.ServiceTypeTURN)
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

// TURNInstanceConfig holds configuration for spawning a TURN instance
type TURNInstanceConfig struct {
	Namespace       string
	NodeID          string
	ListenAddr      string // e.g., "0.0.0.0:3478"
	TURNSListenAddr string // e.g., "0.0.0.0:5349" (TURNS over TLS/TCP)
	PublicIP        string // Public IP for TURN relay allocations
	Realm           string // TURN realm (typically base domain)
	AuthSecret      string // HMAC-SHA1 shared secret
	RelayPortStart  int    // Start of relay port range
	RelayPortEnd    int    // End of relay port range
	TURNDomain      string // TURN domain for Let's Encrypt cert (e.g., "turn.ns-myapp.orama-devnet.network")
	// StealthDomain is the neutral stealth TURNS host (feat-124). When set,
	// the TURN server carries a second Let's Encrypt cert for this name and
	// serves it to TLS clients whose SNI matches — the path the SNI router
	// forwards from :443. Stealth NEVER falls back to a self-signed cert: a
	// cert clients reject is indistinguishable from being blocked.
	StealthDomain string
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

// SpawnTURN starts a TURN instance using systemd
func (s *SystemdSpawner) SpawnTURN(ctx context.Context, namespace, nodeID string, cfg TURNInstanceConfig) error {
	s.logger.Info("Spawning TURN via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID),
		zap.String("listen_addr", cfg.ListenAddr),
		zap.String("public_ip", cfg.PublicIP))

	// Guard (bugboard #846): the TURN server hard-fails on an empty public_ip
	// ("turn.public_ip: must not be empty") and systemd then crash-loops it
	// forever. A crash-looped TURN means (a) zero ICE relay candidates for any
	// client round-robined to this node, and (b) SpawnTURN never reaches the
	// AddWebRTCRules call below, so the relay UDP ports stay firewalled. The
	// caller MUST resolve the node's public IP before spawning — fail loudly
	// here instead of writing a config that is guaranteed to crash-loop.
	if cfg.PublicIP == "" {
		return fmt.Errorf("refusing to spawn TURN for namespace %s on node %s: public_ip is empty (would crash-loop the TURN server); the caller must resolve the node's public IP first", namespace, nodeID)
	}

	// Create config directory
	configDir := filepath.Join(s.namespaceBase, namespace, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, fmt.Sprintf("turn-%s.yaml", nodeID))

	// Provision TLS cert for TURNS — Let's Encrypt via Caddy first (idempotent,
	// also upgrades nodes stuck on the self-signed fallback), self-signed as
	// the primary-domain fallback only.
	var certPath, keyPath string
	if cfg.TURNSListenAddr != "" {
		var certErr error
		certPath, keyPath, certErr = s.resolveTURNSCert(namespace, cfg.TURNDomain, cfg.Realm, cfg.PublicIP, configDir, true)
		if certErr != nil {
			s.logger.Warn("Failed to resolve TURNS cert, TURNS will be disabled",
				zap.String("namespace", namespace),
				zap.Error(certErr))
			cfg.TURNSListenAddr = "" // Disable TURNS if no cert is available
		}
	}

	// Stealth TURNS cert (feat-124): requires a working TURNS listener and a
	// CA-valid cert — hard error, never a silent downgrade, because the
	// operator explicitly enabled stealth and a half-working stealth endpoint
	// is invisible until a censored-region user fails to connect.
	var stealthCertPath, stealthKeyPath string
	if cfg.StealthDomain != "" {
		// Security: the stealth domain arrives over the spawn protocol (mesh
		// peers gated only by the static internal-auth header). Pin it to the
		// deterministic derivation so a forged value can't select cert
		// material for an attacker-chosen name. cfg.Realm is the base domain
		// on every TURN spawn site.
		if cfg.Realm == "" {
			return fmt.Errorf("stealth TURNS for namespace %s requires a base domain (realm) to locate the wildcard cert", namespace)
		}
		want := turn.StealthHostForNamespace(cfg.Namespace, cfg.Realm)
		if cfg.StealthDomain != want {
			return fmt.Errorf("stealth domain %q does not match the derived host %q for namespace %s — refusing to provision", cfg.StealthDomain, want, cfg.Namespace)
		}
		if cfg.TURNSListenAddr == "" {
			return fmt.Errorf("stealth TURNS for namespace %s requires an active TURNS listener (no TLS cert/listener available)", namespace)
		}
		var stealthErr error
		stealthCertPath, stealthKeyPath, stealthErr = s.resolveStealthCert(cfg.StealthDomain, cfg.Realm)
		if stealthErr != nil {
			return fmt.Errorf("failed to resolve stealth TURNS cert for namespace %s: %w", namespace, stealthErr)
		}
	}

	// Build TURN YAML config
	turnConfig := turn.Config{
		ListenAddr:      cfg.ListenAddr,
		TURNSListenAddr: cfg.TURNSListenAddr,
		PublicIP:        cfg.PublicIP,
		Realm:           cfg.Realm,
		AuthSecret:      cfg.AuthSecret,
		RelayPortStart:  cfg.RelayPortStart,
		RelayPortEnd:    cfg.RelayPortEnd,
		Namespace:       cfg.Namespace,
	}
	if cfg.TURNSListenAddr != "" {
		turnConfig.TLSCertPath = certPath
		turnConfig.TLSKeyPath = keyPath
	}
	if stealthCertPath != "" {
		turnConfig.StealthDomain = cfg.StealthDomain
		turnConfig.TLSStealthCertPath = stealthCertPath
		turnConfig.TLSStealthKeyPath = stealthKeyPath
	}

	configBytes, err := yaml.Marshal(turnConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal TURN config: %w", err)
	}

	if err := writeConfigAtomic(configPath, configBytes, 0644); err != nil {
		return fmt.Errorf("failed to write TURN config: %w", err)
	}

	s.logger.Info("Created TURN config file",
		zap.String("path", configPath),
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Generate environment file pointing to config
	envVars := map[string]string{
		"TURN_CONFIG": configPath,
	}

	if err := s.systemdMgr.GenerateEnvFile(namespace, nodeID, systemd.ServiceTypeTURN, envVars); err != nil {
		return fmt.Errorf("failed to generate TURN env file: %w", err)
	}

	// Start the systemd service
	if err := s.systemdMgr.StartService(namespace, systemd.ServiceTypeTURN); err != nil {
		return fmt.Errorf("failed to start TURN service: %w", err)
	}

	// Wait for service to be active
	if err := s.waitForService(namespace, systemd.ServiceTypeTURN, 30*time.Second); err != nil {
		return fmt.Errorf("TURN service did not become active: %w", err)
	}

	// Add firewall rules for TURN ports
	fw := production.NewFirewallProvisioner(production.FirewallConfig{})
	if err := fw.AddWebRTCRules(cfg.RelayPortStart, cfg.RelayPortEnd); err != nil {
		s.logger.Warn("Failed to add WebRTC firewall rules (TURN service is running)",
			zap.String("namespace", namespace),
			zap.Error(err))
	}

	s.logger.Info("TURN spawned successfully via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return nil
}

// StopTURN stops a TURN instance
func (s *SystemdSpawner) StopTURN(ctx context.Context, namespace, nodeID string) error {
	s.logger.Info("Stopping TURN via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	err := s.systemdMgr.StopService(namespace, systemd.ServiceTypeTURN)

	// Remove firewall rules for standard TURN ports
	fw := production.NewFirewallProvisioner(production.FirewallConfig{})
	if fwErr := fw.RemoveWebRTCRules(0, 0); fwErr != nil {
		s.logger.Warn("Failed to remove WebRTC firewall rules",
			zap.String("namespace", namespace),
			zap.Error(fwErr))
	}

	// Remove TURN cert block from Caddyfile (if provisioned via Let's Encrypt)
	configDir := filepath.Join(s.namespaceBase, namespace, "configs")
	configPath := filepath.Join(configDir, fmt.Sprintf("turn-%s.yaml", nodeID))
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		var turnCfg turn.Config
		if yaml.Unmarshal(data, &turnCfg) == nil && turnCfg.Realm != "" {
			turnDomain := fmt.Sprintf("turn.ns-%s.%s", namespace, turnCfg.Realm)
			if removeErr := removeTURNCertFromCaddy(turnDomain); removeErr != nil {
				s.logger.Warn("Failed to remove TURN cert from Caddyfile",
					zap.String("namespace", namespace),
					zap.String("domain", turnDomain),
					zap.Error(removeErr))
			}
		}
	}

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
