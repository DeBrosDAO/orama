package namespace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/gatewayspec"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/sfu"
	"github.com/DeBrosOfficial/network/pkg/systemd"
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
	// (caddyServiceStorageDir). Only set in tests so resolveTURNSCert can be
	// exercised without touching /var/lib.
	caddyStorageDirOverride string
}

// wildcardCertPaths returns the cert/key paths for the `*.<baseDomain>` wildcard
// in Caddy's storage, honoring caddyStorageDirOverride when set (tests).
func (s *SystemdSpawner) wildcardCertPaths(baseDomain string) (certPath, keyPath string) {
	if s.caddyStorageDirOverride != "" {
		return locateCaddyCert(s.caddyStorageDirOverride, "wildcard_."+baseDomain)
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

// joinTargetAllowed decides which hosts may receive the rqlite credentials
// during join verification: WireGuard addresses only. A variable so tests can
// admit their loopback servers.
var joinTargetAllowed = auth.IsWireGuardPeer

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

	// The URL can come from a spawn request, and the cluster-wide rqlite
	// credentials go with it: only a WireGuard address may receive them.
	u, err := url.Parse(verifyURL)
	if err != nil || u.Scheme != "http" || !joinTargetAllowed(u.Host) {
		return fmt.Errorf("verify join target for namespace %s: %q is not an http://<wireguard-ip>:<port> address", namespace, verifyURL)
	}

	reqCtx, cancel := context.WithTimeout(ctx, joinVerifyTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(verifyURL, "/")+"/status", nil)
	if err != nil {
		return fmt.Errorf("verify join target for namespace %s: %w", namespace, err)
	}
	// The target runs with -auth like every rqlited; unauthenticated, /status
	// is a 401 whose body does not decode, and the join would be refused.
	user, pass, err := s.readRQLitePassword()
	if err != nil {
		return fmt.Errorf("verify join target for namespace %s: %w", namespace, err)
	}
	req.SetBasicAuth(user, pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("verify join target %s for namespace %s: %w", verifyURL, namespace, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("verify join target %s for namespace %s: /status returned HTTP %d", verifyURL, namespace, resp.StatusCode)
	}

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

// serviceIsActive is the systemd query ensurePortsFree uses to recognise its
// own unit. A variable so tests can exercise the already-running path on a host
// without systemd.
var serviceIsActive = func(m *systemd.Manager, namespace string, serviceType systemd.ServiceType) (bool, error) {
	return m.IsServiceActive(namespace, serviceType)
}

// ownsItsPorts reports whether this call is reconciling a service that is
// already up on exactly the ports it is being asked to bind.
//
// Both halves matter. "Unit is active" alone is not enough: for a Type=simple
// unit that only proves the process exists, so a service still running on a
// previous port allocation would exempt a completely different port block from
// the check. "Every port is in use" alone is not enough either: that is equally
// true when a foreign process holds them, which is bug-276 exactly. Together
// they say the thing that is actually safe to skip — a no-op reconcile of a
// running service — and every other shape falls through to the strict check.
func (s *SystemdSpawner) ownsItsPorts(namespace string, serviceType systemd.ServiceType, ports map[string]int) bool {
	active, err := serviceIsActive(s.systemdMgr, namespace, serviceType)
	if err != nil || !active {
		return false
	}
	for _, port := range ports {
		if port <= 0 {
			continue
		}
		if !portInUse(port) {
			return false
		}
	}
	return true
}

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
//
// "Something else" excludes the unit this call is about to start. Spawning is a
// reconcile, not a one-shot: the boot supervisor calls it again after any
// failure, and on the second call the service started by the first one is
// legitimately holding its own port. Without this check that retry waited ten
// seconds and then reported a port conflict against itself, which no amount of
// retrying could clear.
func (s *SystemdSpawner) ensurePortsFree(namespace string, serviceType systemd.ServiceType, ports map[string]int) error {
	if s.ownsItsPorts(namespace, serviceType, ports) {
		s.logger.Debug("Service already active on the ports it is being asked to bind; not a conflict",
			zap.String("namespace", namespace),
			zap.String("service", string(serviceType)))
		return nil
	}

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

// rqliteJoinArgs is the -join flag set for rqlited, empty when there is
// nothing to join. Every instance runs with -auth, which refuses an anonymous
// join, so the join names the auth file's user that may join.
func rqliteJoinArgs(joinAddresses []string, authFile string) (string, error) {
	if len(joinAddresses) == 0 {
		return "", nil
	}
	joinAs, err := rqlite.JoinUser(authFile)
	if err != nil {
		return "", err
	}
	return "-join " + strings.Join(joinAddresses, ",") + " -join-as " + joinAs, nil
}

// SpawnRQLite starts a RQLite instance using systemd
func (s *SystemdSpawner) SpawnRQLite(ctx context.Context, namespace, nodeID string, cfg rqlite.InstanceConfig) error {
	s.logger.Info("Spawning RQLite via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	// Bugboard #281: a brand-new cluster must not inherit raft state left behind
	// by a previous namespace of the same name. Clearing here (rather than
	// trusting delete to have succeeded) is what makes re-creating a namespace
	// deterministic.
	if cfg.FreshStart {
		// A fresh cluster must never adopt a running unit. ensurePortsFree
		// below treats an already-active service's own port as legitimate, so
		// that a reconcile is idempotent — but "fresh" is the one case where an
		// active unit means a leftover namespace is still live (bugboard #275),
		// and where the clear below would be deleting the raft directory out
		// from under a running rqlited.
		if active, err := serviceIsActive(s.systemdMgr, namespace, systemd.ServiceTypeRQLite); err == nil && active {
			return fmt.Errorf(
				"cannot fresh-start RQLite for namespace %s: its unit is already running — "+
					"a leftover namespace of the same name is still live; stop and delete it before re-creating",
				namespace)
		}

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

	if err := s.ensurePortsFree(namespace, systemd.ServiceTypeRQLite, map[string]int{
		"RQLite HTTP": cfg.HTTPPort,
		"RQLite Raft": cfg.RaftPort,
	}); err != nil {
		return err
	}

	if err := s.verifyJoinTarget(ctx, namespace, cfg.JoinVerifyURL); err != nil {
		return err
	}

	// Generate environment file
	dataDir := cfg.DataDir
	if dataDir == "" {
		dataDir = rqliteUnitDataDir(namespace, nodeID, s.namespaceBase, "")
	}
	authSrc := cfg.AuthFile
	if authSrc == "" {
		authSrc = filepath.Join(s.oramaDir(), "secrets", "rqlite-auth.json")
	}
	authDest, err := rqlite.InstallAuthFile(authSrc, dataDir)
	if err != nil {
		return fmt.Errorf("rqlite auth file missing — refusing to start: %w", err)
	}
	joinArgs, err := rqliteJoinArgs(cfg.JoinAddresses, authDest)
	if err != nil {
		return fmt.Errorf("cannot join RQLite for namespace %s: %w", namespace, err)
	}
	httpAddr, err := rqlite.BindAddr(cfg.HTTPAdvAddress, cfg.HTTPPort)
	if err != nil {
		return fmt.Errorf("rqlite HTTP bind: %w", err)
	}
	raftAddr, err := rqlite.BindAddr(cfg.RaftAdvAddress, cfg.RaftPort)
	if err != nil {
		return fmt.Errorf("rqlite Raft bind: %w", err)
	}
	envVars := map[string]string{
		"HTTP_ADDR":     httpAddr,
		"RAFT_ADDR":     raftAddr,
		"HTTP_ADV_ADDR": cfg.HTTPAdvAddress,
		"RAFT_ADV_ADDR": cfg.RaftAdvAddress,
		"JOIN_ARGS":     joinArgs,
		"NODE_ID":       nodeID,
		"DATA_DIR":      dataDir,
		"EXTRA_ARGS":    strings.TrimSpace(cfg.ExtraArgs + " -auth " + authDest),
	}

	if err := s.systemdMgr.GenerateEnvFile(namespace, nodeID, systemd.ServiceTypeRQLite, envVars); err != nil {
		return fmt.Errorf("failed to generate RQLite env file: %w", err)
	}

	// Start the systemd service
	if err := s.systemdMgr.StartService(namespace, systemd.ServiceTypeRQLite); err != nil {
		return fmt.Errorf("failed to start RQLite service: %w", err)
	}

	// Wait for service to be active
	if err := s.waitForService(ctx, namespace, systemd.ServiceTypeRQLite, 30*time.Second); err != nil {
		return fmt.Errorf("RQLite service did not become active: %w", err)
	}

	s.logger.Info("RQLite spawned successfully via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	return nil
}

// SpawnOlric starts an Olric instance using systemd
// Olric's on-disk config, shared by the spawn and reconcile paths so the two
// cannot drift in what they consider a complete config.
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

// olricPartitionCount is tuned for namespace clusters, against Olric's 256
// default.
const olricPartitionCount = 12

func buildOlricConfig(cfg olric.InstanceConfig) olricConfig {
	return olricConfig{
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
		PartitionCount: olricPartitionCount,
	}
}

// olricConfigInSync reports whether the on-disk config already expresses the
// desired one.
//
// Peers are compared as a SET. Their order comes from a database query and is
// not meaningful to Olric, so comparing slices directly would report drift on
// every sweep and restart the cache in a loop.
func olricConfigInSync(onDisk, desired olricConfig) bool {
	if onDisk.Server != desired.Server {
		return false
	}
	if onDisk.Memberlist.Environment != desired.Memberlist.Environment ||
		onDisk.Memberlist.BindAddr != desired.Memberlist.BindAddr ||
		onDisk.Memberlist.BindPort != desired.Memberlist.BindPort {
		return false
	}
	if onDisk.PartitionCount != desired.PartitionCount {
		return false
	}
	return sameStringSet(onDisk.Memberlist.Peers, desired.Memberlist.Peers)
}

// sameStringSet compares two lists ignoring ORDER but not multiplicity.
//
// Order is meaningless here — it comes from a database query — so ignoring it
// is what stops every sweep reporting drift. Duplicates are a different matter:
// the desired list is generated fresh and never contains one, so a duplicate on
// disk is residue worth rewriting rather than accepting.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
		if counts[v] < 0 {
			return false
		}
	}
	return true
}

// ReconcileOlric rewrites this node's Olric config when it has drifted from the
// desired one. It does not restart Olric.
//
// Olric is a clustered, stateful service, and the same reconcile runs on every
// node: systemd.Manager.StartService never restarts a running one as a side
// effect, so the rewritten config takes effect at Olric's next deliberate
// (rolling) restart. A running member does not need it sooner — memberlist
// finds and drops a departed peer on its own; `memberlist.peers` is only read
// when Olric starts and joins.
//
// The counterpart to ReconcileGateway, which existed while this did not — so
// when a namespace member was replaced, the survivors' `memberlist.peers` kept
// the dead node's overlay address indefinitely and nothing but a hand-edit
// removed it, and the next restart tried to join the dead address.
func (s *SystemdSpawner) ReconcileOlric(ctx context.Context, namespace, nodeID string, cfg olric.InstanceConfig) error {
	configPath := filepath.Join(s.namespaceBase, namespace, "configs", fmt.Sprintf("olric-%s.yaml", nodeID))

	existing, err := os.ReadFile(configPath)
	if err != nil {
		// No readable config to compare against. Restarting a healthy Olric on
		// that basis would be guessing; a missing config is the cold-spawn
		// path's problem.
		return fmt.Errorf("read olric config for reconcile: %w", err)
	}

	var onDisk olricConfig
	if err := yaml.Unmarshal(existing, &onDisk); err != nil {
		return fmt.Errorf("parse olric config for reconcile: %w", err)
	}

	desired := buildOlricConfig(cfg)
	if olricConfigInSync(onDisk, desired) {
		return nil
	}

	s.logger.Info("Olric config drifted from desired; rewriting it (applies at Olric's next rolling restart)",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID),
		zap.Strings("ondisk_peers", onDisk.Memberlist.Peers),
		zap.Strings("desired_peers", desired.Memberlist.Peers))

	return s.SpawnOlric(ctx, namespace, nodeID, cfg)
}

func (s *SystemdSpawner) SpawnOlric(ctx context.Context, namespace, nodeID string, cfg olric.InstanceConfig) error {
	s.logger.Info("Spawning Olric via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	if err := s.ensurePortsFree(namespace, systemd.ServiceTypeOlric, map[string]int{
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

	config := buildOlricConfig(cfg)

	configBytes, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal Olric config: %w", err)
	}

	if err := s.writeServiceConfig(namespace, systemd.ServiceTypeOlric, configPath, configBytes, 0644); err != nil {
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
	if err := s.waitForService(ctx, namespace, systemd.ServiceTypeOlric, 30*time.Second); err != nil {
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
func (s *SystemdSpawner) SpawnGateway(ctx context.Context, namespace, nodeID string, cfg gatewayspec.InstanceConfig) error {
	s.logger.Info("Spawning Gateway via systemd",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID))

	if err := s.ensurePortsFree(namespace, systemd.ServiceTypeGateway, map[string]int{"Gateway HTTP": cfg.HTTPPort}); err != nil {
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
	gatewayConfig, err := s.gatewayYAMLFor(namespace, cfg)
	if err != nil {
		return err
	}

	configBytes, err := yaml.Marshal(gatewayConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal Gateway config: %w", err)
	}

	// 0600: the gateway YAML embeds the secrets encryption key (bugboard
	// #837), so it must not be world/group readable.
	if err := s.writeServiceConfig(namespace, systemd.ServiceTypeGateway, configPath, configBytes, 0600); err != nil {
		return fmt.Errorf("failed to write Gateway config: %w", err)
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
	if err := s.waitForService(ctx, namespace, systemd.ServiceTypeGateway, 30*time.Second); err != nil {
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
func (s *SystemdSpawner) RestartGateway(ctx context.Context, namespace, nodeID string, cfg gatewayspec.InstanceConfig) error {
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

// gatewayYAMLFor is the gateway YAML SpawnGateway writes for cfg: cfg with the
// cluster rqlite credentials applied to its DSNs, the host's API-key HMAC
// secret, the address the gateway listens on and the cluster secret path.
//
// SpawnGateway writes it and ReconcileGateway compares against it, so the two
// cannot disagree about what "in sync" means. They used to: reconcile compared
// the on-disk (credentialed) YAML against the bare desired config and saw
// drift on every sweep.
func (s *SystemdSpawner) gatewayYAMLFor(namespace string, cfg gatewayspec.InstanceConfig) (gatewayspec.GatewayYAMLConfig, error) {
	// Bugboard #160: a namespace gateway with no HMAC secret can never
	// authenticate anything — fail loud (see SpawnGateway).
	hmacSecret, err := s.readAPIKeyHMACSecret()
	if err != nil {
		return gatewayspec.GatewayYAMLConfig{}, err
	}
	listenAddr, err := gatewayListenAddr(cfg.Namespace, cfg.HTTPPort)
	if err != nil {
		return gatewayspec.GatewayYAMLConfig{}, err
	}
	user, pass, err := s.readRQLitePassword()
	if err != nil {
		return gatewayspec.GatewayYAMLConfig{}, err
	}
	cfg.RQLiteUsername = user
	cfg.RQLitePassword = pass
	// Host layout, not caller input: every gateway on this host gets its own
	// private directory, so their signing keys can never collide.
	cfg.StateDir = constants.GatewayStateDir(s.namespaceBase, namespace)
	if cfg.RQLiteDSN, err = withRQLiteCredentials(cfg.RQLiteDSN, user, pass); err != nil {
		return gatewayspec.GatewayYAMLConfig{}, fmt.Errorf("gateway %s rqlite_dsn: %w", namespace, err)
	}
	// Empty for the index gateway, which is itself the global registry.
	if cfg.GlobalRQLiteDSN != "" {
		if cfg.GlobalRQLiteDSN, err = withRQLiteCredentials(cfg.GlobalRQLiteDSN, user, pass); err != nil {
			return gatewayspec.GatewayYAMLConfig{}, fmt.Errorf("gateway %s global_rqlite_dsn: %w", namespace, err)
		}
	}
	return gatewayYAMLFromInstance(cfg, hmacSecret, s.clusterSecretPath, listenAddr), nil
}

// gatewayWebRTCInSync reports whether the WebRTC block already on disk
// matches the desired gateway config — i.e. no restart is needed.
// Compares only the WebRTC-relevant fields (bugboard #25 drift surface).
// Pure function so the reconcile decision is unit-testable without files
// or systemd.
func gatewayWebRTCInSync(onDisk gatewayspec.GatewayYAMLWebRTC, cfg gatewayspec.InstanceConfig) bool {
	return onDisk.Enabled == cfg.WebRTCEnabled &&
		onDisk.SFUPort == cfg.SFUPort &&
		onDisk.TURNSecret == cfg.TURNSecret &&
		onDisk.TURNDomain == cfg.TURNDomain &&
		onDisk.TURNStealthDomain == cfg.TURNStealthDomain
}

func (s *SystemdSpawner) readRQLitePassword() (user, pass string, err error) {
	path := filepath.Join(s.oramaDir(), "secrets", "rqlite-password")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read rqlite password at %s (required once rqlited -auth is on): %w", path, err)
	}
	pass = strings.TrimSpace(string(b))
	if pass == "" {
		return "", "", fmt.Errorf("rqlite password file %s is empty", path)
	}
	return "orama", pass, nil
}

func (s *SystemdSpawner) readAPIKeyHMACSecret() (string, error) {
	path := filepath.Join(s.oramaDir(), "secrets", apiKeyHMACSecretFileName)
	secretBytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read API-key HMAC secret at %s (required for namespace gateway auth): %w", path, err)
	}
	secret := strings.TrimSpace(string(secretBytes))
	if secret == "" {
		return "", fmt.Errorf("API-key HMAC secret file %s is empty; namespace gateway cannot authenticate without it", path)
	}
	return secret, nil
}

// gatewayYAMLFromInstance is the single builder SpawnGateway and
// ReconcileGateway share. Adding a field to GatewayYAMLConfig without
// putting it here makes spawn and reconcile diverge; the field-coverage
// test in reconcile_gateway_test.go fails when that happens.
func gatewayYAMLFromInstance(cfg gatewayspec.InstanceConfig, hmacSecret, clusterSecretPath, listenAddr string) gatewayspec.GatewayYAMLConfig {
	return gatewayspec.GatewayYAMLConfig{
		ListenAddr:            listenAddr,
		ClientNamespace:       cfg.Namespace,
		RQLiteDSN:             cfg.RQLiteDSN,
		GlobalRQLiteDSN:       cfg.GlobalRQLiteDSN,
		RQLiteUsername:        cfg.RQLiteUsername,
		RQLitePassword:        cfg.RQLitePassword,
		DomainName:            cfg.BaseDomain,
		OlricServers:          cfg.OlricServers,
		OlricTimeout:          cfg.OlricTimeout.String(),
		IPFSClusterAPIURL:     cfg.IPFSClusterAPIURL,
		IPFSAPIURL:            cfg.IPFSAPIURL,
		IPFSTimeout:           cfg.IPFSTimeout.String(),
		IPFSReplicationFactor: cfg.IPFSReplicationFactor,
		ClusterSecretPath:     clusterSecretPath,
		SecretsEncryptionKey:  cfg.SecretsEncryptionKey,
		NtfyBaseURL:           cfg.NtfyBaseURL,
		APIKeyHMACSecret:      hmacSecret,
		StateDir:              cfg.StateDir,
		WebRTC: gatewayspec.GatewayYAMLWebRTC{
			Enabled:           cfg.WebRTCEnabled,
			SFUPort:           cfg.SFUPort,
			TURNDomain:        cfg.TURNDomain,
			TURNSecret:        cfg.TURNSecret,
			TURNStealthDomain: cfg.TURNStealthDomain,
		},
	}
}

func timeoutEqual(a, b string) bool {
	return normalizeTimeout(a) == normalizeTimeout(b)
}

func normalizeTimeout(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" || s == "0s" {
		return ""
	}
	return s
}

func stringSetEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// gatewayYAMLEqual compares every spawn-written GatewayYAMLConfig field.
// Olric server order is ignored (discovery can reshuffle). Empty / "0s"
// timeouts compare equal so omitempty on-disk values match a zero duration.
func gatewayYAMLEqual(a, b gatewayspec.GatewayYAMLConfig) bool {
	return a.ListenAddr == b.ListenAddr &&
		a.ClientNamespace == b.ClientNamespace &&
		a.RQLiteDSN == b.RQLiteDSN &&
		a.GlobalRQLiteDSN == b.GlobalRQLiteDSN &&
		a.RQLiteUsername == b.RQLiteUsername &&
		a.RQLitePassword == b.RQLitePassword &&
		stringSetEqual(a.BootstrapPeers, b.BootstrapPeers) &&
		a.EnableHTTPS == b.EnableHTTPS &&
		a.DomainName == b.DomainName &&
		a.TLSCacheDir == b.TLSCacheDir &&
		stringSetEqual(a.OlricServers, b.OlricServers) &&
		timeoutEqual(a.OlricTimeout, b.OlricTimeout) &&
		a.IPFSClusterAPIURL == b.IPFSClusterAPIURL &&
		a.IPFSAPIURL == b.IPFSAPIURL &&
		timeoutEqual(a.IPFSTimeout, b.IPFSTimeout) &&
		a.IPFSReplicationFactor == b.IPFSReplicationFactor &&
		a.WebRTC.Enabled == b.WebRTC.Enabled &&
		a.WebRTC.SFUPort == b.WebRTC.SFUPort &&
		a.WebRTC.TURNDomain == b.WebRTC.TURNDomain &&
		a.WebRTC.TURNSecret == b.WebRTC.TURNSecret &&
		a.WebRTC.TURNStealthDomain == b.WebRTC.TURNStealthDomain &&
		a.SecretsEncryptionKey == b.SecretsEncryptionKey &&
		a.ClusterSecretPath == b.ClusterSecretPath &&
		a.APIKeyHMACSecret == b.APIKeyHMACSecret &&
		a.NtfyBaseURL == b.NtfyBaseURL &&
		a.StateDir == b.StateDir
}

// gatewayConfigInSync reports whether on-disk YAML matches what spawn would
// write for cfg. Comparison is exhaustive over GatewayYAMLConfig (bugboard
// #165): a new YAML field that spawn writes cannot silently skip reconcile.
func gatewayConfigInSync(onDisk gatewayspec.GatewayYAMLConfig, cfg gatewayspec.InstanceConfig, hmacSecret, clusterSecretPath, listenAddr string) bool {
	return gatewayYAMLEqual(onDisk, gatewayYAMLFromInstance(cfg, hmacSecret, clusterSecretPath, listenAddr))
}

// ReconcileGateway is the WARM counterpart to SpawnGateway: when a
// namespace gateway is already running, this compares its on-disk config
// against what SpawnGateway would write for `cfg` and restarts it ONLY when
// they differ (WebRTC block, membership, DSNs, secrets, ...).
//
// Bugboard #25: the from-disk restore skips healthy gateways, so a
// gateway that lost its webrtc block on a prior restart (while staying
// healthy) never gets its config regenerated — leaving SFU/TURN services
// running but the gateway with no turn_secret/sfu_port (credentials
// configured:false, /v1/webrtc/turn/credentials 404). The cold-spawn
// self-heal only fires when the gateway happens to be down during
// restore. This closes that gap for the healthy case.
//
// Idempotent: returns nil WITHOUT restarting when the on-disk YAML already
// equals what SpawnGateway would write for cfg (gatewayYAMLFor) — so it does
// not cause a restart loop on every sweep or boot. The comparison covers every
// YAML field (bugboard #165); Olric server order and empty/zero timeouts are
// not drift.
func (s *SystemdSpawner) ReconcileGateway(ctx context.Context, namespace, nodeID string, cfg gatewayspec.InstanceConfig) error {
	onDisk, err := s.readGatewayYAML(namespace, nodeID)
	if err != nil {
		// No readable config to compare against — don't blindly restart a
		// healthy gateway; absence of the config file is a different
		// problem the caller's cold-spawn path handles.
		return err
	}

	// The desired YAML is exactly what SpawnGateway would write for cfg —
	// credentials, HMAC secret and listen address included — so a gateway
	// SpawnGateway just wrote compares in sync. An error here (no HMAC secret,
	// no rqlite password, no WireGuard IP) is returned without restarting:
	// SpawnGateway would fail on the same thing, and stopping a running
	// gateway to find that out would take it down for nothing.
	desired, err := s.gatewayYAMLFor(namespace, cfg)
	if err != nil {
		return fmt.Errorf("resolve desired gateway config for %s: %w", namespace, err)
	}
	if gatewayYAMLEqual(onDisk, desired) {
		return nil
	}

	// Drift flags are bools — never log secret material (bugboard #165 / #837).
	s.logger.Info("Gateway config drifted from desired; reconciling (rewrite + restart)",
		zap.String("namespace", namespace),
		zap.String("node_id", nodeID),
		zap.Bool("ondisk_enabled", onDisk.WebRTC.Enabled),
		zap.Int("ondisk_sfu_port", onDisk.WebRTC.SFUPort),
		zap.Bool("desired_enabled", cfg.WebRTCEnabled),
		zap.Int("desired_sfu_port", cfg.SFUPort),
		zap.Bool("olric_servers_drifted", !stringSetEqual(onDisk.OlricServers, desired.OlricServers)),
		zap.Bool("listen_addr_drifted", onDisk.ListenAddr != desired.ListenAddr),
		zap.Bool("secrets_key_drifted", onDisk.SecretsEncryptionKey != desired.SecretsEncryptionKey),
		zap.Bool("hmac_secret_drifted", onDisk.APIKeyHMACSecret != desired.APIKeyHMACSecret),
		zap.Bool("state_dir_drifted", onDisk.StateDir != desired.StateDir),
		zap.Bool("rqlite_drifted", onDisk.RQLiteDSN != desired.RQLiteDSN || onDisk.GlobalRQLiteDSN != desired.GlobalRQLiteDSN))
	return s.RestartGateway(ctx, namespace, nodeID, cfg)
}

// gatewayMembership is the part of a tenant gateway's config that follows
// cluster membership: its port block and the Olric servers it talks to.
type gatewayMembership struct {
	HTTPPort     int
	OlricServers []string
}

// ReconcileGatewayMembership brings a running tenant gateway in line with the
// live membership. Everything else — DSNs, secrets, WebRTC, domains — is taken
// from the config the gateway is running with: the membership sweep knows
// only membership, and rebuilding the rest from it produced a skeleton config
// that never matched, so every sweep restarted the gateway into a config it
// could not start with.
func (s *SystemdSpawner) ReconcileGatewayMembership(ctx context.Context, namespace, nodeID string, m gatewayMembership) error {
	onDisk, err := s.readGatewayYAML(namespace, nodeID)
	if err != nil {
		return err
	}
	cfg, err := instanceFromGatewayYAML(onDisk, nodeID)
	if err != nil {
		return fmt.Errorf("gateway %s: %w", namespace, err)
	}
	cfg.HTTPPort = m.HTTPPort
	cfg.OlricServers = m.OlricServers
	return s.ReconcileGateway(ctx, namespace, nodeID, cfg)
}

// readGatewayYAML reads the gateway config SpawnGateway wrote for nodeID.
func (s *SystemdSpawner) readGatewayYAML(namespace, nodeID string) (gatewayspec.GatewayYAMLConfig, error) {
	configPath := filepath.Join(s.namespaceBase, namespace, "configs", fmt.Sprintf("gateway-%s.yaml", nodeID))
	existing, err := os.ReadFile(configPath)
	if err != nil {
		return gatewayspec.GatewayYAMLConfig{}, fmt.Errorf("read gateway config for reconcile: %w", err)
	}
	var onDisk gatewayspec.GatewayYAMLConfig
	if err := yaml.Unmarshal(existing, &onDisk); err != nil {
		return gatewayspec.GatewayYAMLConfig{}, fmt.Errorf("parse gateway config %s for reconcile: %w", configPath, err)
	}
	return onDisk, nil
}

// instanceFromGatewayYAML is the inverse of gatewayYAMLFromInstance for every
// field the YAML carries, so a config read back from disk re-renders to the
// same YAML. The host-level fields (HMAC secret, cluster secret path) are not
// part of InstanceConfig; gatewayYAMLFor re-reads them from the host.
func instanceFromGatewayYAML(y gatewayspec.GatewayYAMLConfig, nodeID string) (gatewayspec.InstanceConfig, error) {
	_, portStr, err := net.SplitHostPort(y.ListenAddr)
	if err != nil {
		return gatewayspec.InstanceConfig{}, fmt.Errorf("listen_addr %q: %w", y.ListenAddr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return gatewayspec.InstanceConfig{}, fmt.Errorf("listen_addr %q has an invalid port", y.ListenAddr)
	}
	olricTimeout, err := parseYAMLDuration(y.OlricTimeout)
	if err != nil {
		return gatewayspec.InstanceConfig{}, fmt.Errorf("olric_timeout: %w", err)
	}
	ipfsTimeout, err := parseYAMLDuration(y.IPFSTimeout)
	if err != nil {
		return gatewayspec.InstanceConfig{}, fmt.Errorf("ipfs_timeout: %w", err)
	}
	return gatewayspec.InstanceConfig{
		Namespace:             y.ClientNamespace,
		NodeID:                nodeID,
		HTTPPort:              port,
		BaseDomain:            y.DomainName,
		RQLiteDSN:             y.RQLiteDSN,
		GlobalRQLiteDSN:       y.GlobalRQLiteDSN,
		RQLiteUsername:        y.RQLiteUsername,
		RQLitePassword:        y.RQLitePassword,
		OlricServers:          y.OlricServers,
		OlricTimeout:          olricTimeout,
		IPFSClusterAPIURL:     y.IPFSClusterAPIURL,
		IPFSAPIURL:            y.IPFSAPIURL,
		IPFSTimeout:           ipfsTimeout,
		IPFSReplicationFactor: y.IPFSReplicationFactor,
		WebRTCEnabled:         y.WebRTC.Enabled,
		SFUPort:               y.WebRTC.SFUPort,
		TURNDomain:            y.WebRTC.TURNDomain,
		TURNSecret:            y.WebRTC.TURNSecret,
		TURNStealthDomain:     y.WebRTC.TURNStealthDomain,
		SecretsEncryptionKey:  y.SecretsEncryptionKey,
		NtfyBaseURL:           y.NtfyBaseURL,
		StateDir:              y.StateDir,
	}, nil
}

// parseYAMLDuration reads a duration gatewayYAMLFromInstance wrote; empty is 0.
func parseYAMLDuration(v string) (time.Duration, error) {
	if normalizeTimeout(v) == "" {
		return 0, nil
	}
	return time.ParseDuration(v)
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

// sfuConfigMode is the mode of an SFU config file.
//
// It carries the namespace's TURN shared secret and its rqlite DSN, which has
// the database password in it. The file was written 0644, so any local account
// on the node could mint TURN credentials for the namespace and read its
// database.
const sfuConfigMode = 0600

// writeSFUConfig renders and writes one SFU config.
//
// The write is atomic: a 0600 temp file is renamed over the path, so a file an
// earlier release left at 0644 is replaced rather than left as it was.
func writeSFUConfig(configPath string, cfg SFUInstanceConfig) error {
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
	if err := writeConfigAtomic(configPath, configBytes, sfuConfigMode); err != nil {
		return fmt.Errorf("failed to write SFU config: %w", err)
	}
	return nil
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
	user, pass, err := s.readRQLitePassword()
	if err != nil {
		return err
	}
	if cfg.RQLiteDSN, err = withRQLiteCredentials(cfg.RQLiteDSN, user, pass); err != nil {
		return fmt.Errorf("sfu %s rqlite_dsn: %w", namespace, err)
	}
	if err := writeSFUConfig(configPath, cfg); err != nil {
		return err
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
	if err := s.waitForService(ctx, namespace, systemd.ServiceTypeSFU, 30*time.Second); err != nil {
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

// resolveTURNSCert returns the Caddy `*.<baseDomain>` wildcard certificate for
// the shared TURN server's TURNS listener.
//
// The wildcard covers every host that listener answers for: each tenant's
// single-label turn-<ns>.<base> (turn.TLSHostForNamespace) and cdn-<hash>.<base>
// stealth host. It is the only source. Per-domain Let's Encrypt provisioning by
// appending to the Caddyfile could never work from orama-node
// (ProtectSystem=strict makes /etc/caddy read-only), and a self-signed pair is
// what clients reject — for a stealth host, indistinguishable from being
// blocked. With no wildcard on disk this is an error, and the caller leaves
// TURNS off.
func (s *SystemdSpawner) resolveTURNSCert(baseDomain string) (string, string, error) {
	if baseDomain == "" {
		return "", "", fmt.Errorf("TURNS cert: no base domain configured, so there is no *.<base> wildcard cert to use")
	}
	certPath, keyPath := s.wildcardCertPaths(baseDomain)
	if _, err := os.Stat(certPath); err != nil {
		return "", "", fmt.Errorf("TURNS cert: Caddy wildcard cert for *.%s not found at %s (is the gateway HTTPS wildcard provisioned on this node?): %w", baseDomain, certPath, err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		return "", "", fmt.Errorf("TURNS cert: Caddy wildcard key for *.%s not found at %s: %w", baseDomain, keyPath, err)
	}
	s.logger.Info("Using Caddy wildcard cert for TURNS",
		zap.String("base_domain", baseDomain),
		zap.String("cert_path", certPath))
	return certPath, keyPath, nil
}

// resolveStealthCert resolves the TLS cert/key for the stealth TURNS host by
// reusing Caddy's existing `*.<baseDomain>` wildcard certificate (feat-124).
//
// The stealth host is a single-label subdomain of the base domain
// (cdn-<hash>.<baseDomain>), so the wildcard the gateway already provisions
// for HTTPS covers it. Caddy renews the wildcard; the TURN cert reloader
// hot-reloads it from storage.
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
	certPath, keyPath := s.wildcardCertPaths(baseDomain)
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
	// The units' env files live in the root-owned unit-env tree, not in the
	// data directory (pkg/unitenv); they go with the namespace.
	if err := s.systemdMgr.RemoveNamespaceEnv(namespace); err != nil {
		return fmt.Errorf("delete namespace %s env files: %w", namespace, err)
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
// waitForService polls until the unit reports active, the timeout elapses, or
// ctx is cancelled.
//
// The context matters at shutdown: this used to poll with a bare time.Sleep,
// so a spawn already in flight when the node was asked to stop kept running for
// the full 30s per call — long enough for the node's teardown to race the
// reconcile that was still writing to it.
func (s *SystemdSpawner) waitForService(ctx context.Context, namespace string, serviceType systemd.ServiceType, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		active, err := s.systemdMgr.IsServiceActive(namespace, serviceType)
		if err != nil {
			return fmt.Errorf("failed to check service status: %w", err)
		}

		if active {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s/%s to become active: %w", namespace, serviceType, ctx.Err())
		case <-time.After(serviceActivePollInterval):
		}
	}

	return fmt.Errorf("service did not become active within %v", timeout)
}

// serviceActivePollInterval is how often waitForService re-checks systemd.
const serviceActivePollInterval = 1 * time.Second

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

// gatewayListenAddr is where a namespace gateway binds.
//
// Every gateway binds the overlay address, not every interface. They used to
// bind `:port`, so the only thing between a gateway and the internet was a
// firewall rule — and a firewall rule is a thing that can be wrong, whereas a
// listener that is not on a public interface cannot be reached from one
// however the rules are written.
//
// The index gateway is also reached on this host — by Caddy, which
// reverse-proxies to localhost, by Caddy's DNS-01 calls and by the CLI — so
// the gateway binary adds a loopback listener on the same port for it
// (cmd/gateway listenAddrs). Other nodes reach it on the overlay.
func gatewayListenAddr(namespace string, port int) (string, error) {
	ip, err := overlayIP()
	if err != nil {
		// Refusing is right. A gateway that cannot find the overlay is a node
		// whose WireGuard is not up, and binding every interface instead would
		// silently put a tenant's gateway on the public one.
		return "", fmt.Errorf("cannot bind the %s gateway to the overlay: WireGuard is not up on this node (%w)", namespace, err)
	}
	return fmt.Sprintf("%s:%d", ip, port), nil
}

// overlayIP is this node's address on the WireGuard mesh. A variable so a test
// can run without an interface; nothing else replaces it.
var overlayIP = getWireGuardIP

// isIndexNamespace reports whether a namespace is the cluster's own rather than
// a tenant's.
func isIndexNamespace(namespace string) bool {
	switch strings.TrimSpace(namespace) {
	case "", "index", "default":
		return true
	}
	return false
}

// writeServiceConfig writes a service's config file at mode and, when the
// content changed, marks the service so the next StartService restarts a
// running unit onto it. The mode is converged explicitly: WriteFile's mode
// only applies on create, and an older release wrote some of these 0644.
func (s *SystemdSpawner) writeServiceConfig(namespace string, st systemd.ServiceType, path string, data []byte, mode os.FileMode) error {
	existing, err := os.ReadFile(path)
	unchanged := err == nil && bytes.Equal(existing, data)
	if !unchanged {
		if err := os.WriteFile(path, data, mode); err != nil {
			return err
		}
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("set permissions on %s: %w", path, err)
	}
	if !unchanged {
		s.systemdMgr.MarkConfigChanged(namespace, st)
	}
	return nil
}
