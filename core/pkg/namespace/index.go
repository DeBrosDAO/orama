package namespace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gatewayspec"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// raftNonVoterFlag makes rqlited join as a non-voter.
const raftNonVoterFlag = "-raft-non-voter"

// IsIndexGateway reports whether this process is the core/index gateway.
func IsIndexGateway(clientNamespace string) bool {
	return clientNamespace == BlueprintNameIndex
}

// rqliteUnitDataDir is the rqlited data path written into DATA_DIR.
// Index always uses the core dir (adopt in place). Tenants use namespaces/<ns>/rqlite/<node>.
func rqliteUnitDataDir(namespace, nodeID, namespaceBase, coreRQLiteDir string) string {
	if namespace == BlueprintNameIndex {
		return coreRQLiteDir
	}
	return filepath.Join(namespaceBase, namespace, "rqlite", nodeID)
}

// IndexSupervisor starts orama-namespace-{rqlite,olric,gateway}@index locally.
// It does not call ClusterManager or SelectNodesForCluster — index is this machine.
type IndexSupervisor struct {
	oramaDir      string
	dataDir       string
	namespaceBase string
	spawner       *SystemdSpawner
	systemdMgr    *systemd.Manager
	logger        *zap.Logger

	// What EnsureRQLite reads and does. NewIndexSupervisor sets the real ones;
	// they are fields so its decision can be tested without a data directory,
	// a running rqlited or systemd.
	raftState    func(ctx context.Context, dataDir, httpAddr, authFile string) (bool, error)
	raftIdentity func(dataDir, peerID, raftAdvAddress string, hasState bool) (rqlite.RaftIdentity, error)
	spawnRQLite  func(ctx context.Context, namespace, nodeID string, cfg rqlite.InstanceConfig) error
}

// NewIndexSupervisor builds a supervisor. oramaDir is ~/.orama (parent of data/).
func NewIndexSupervisor(oramaDir string, logger *zap.Logger) *IndexSupervisor {
	dataDir := filepath.Join(oramaDir, "data")
	namespaceBase := filepath.Join(dataDir, "namespaces")
	if logger == nil {
		logger = zap.NewNop()
	}
	spawner := NewSystemdSpawner(namespaceBase, filepath.Join(oramaDir, "secrets", "cluster-secret"), logger)
	return &IndexSupervisor{
		oramaDir:      oramaDir,
		dataDir:       dataDir,
		namespaceBase: namespaceBase,
		spawner:       spawner,
		systemdMgr:    systemd.NewManager(namespaceBase, logger),
		logger:        logger.With(zap.String("component", "index-supervisor")),
		raftState:     rqlite.NodeHasRaftState,
		raftIdentity:  rqlite.ResolveRaftIdentity,
		spawnRQLite:   spawner.SpawnRQLite,
	}
}

// CoreRQLiteDir is the live raft directory (~/.orama/data/rqlite). Never namespaces/index/rqlite.
func (s *IndexSupervisor) CoreRQLiteDir() string {
	return filepath.Join(s.dataDir, "rqlite")
}

// EnsureRQLite writes @index env (DATA_DIR = existing core raft) and starts the unit.
//
// A node with raft state restarts into the cluster it has; one without joins
// joinAddress. Passing -join to a member would make its every restart depend on
// that one address answering — except when the configuration holds this node
// at an address it no longer listens on, where joining again is how the leader
// learns the new one. Without state, without a join address and without a
// cluster membership record — a fresh genesis install — rqlited bootstraps a
// new cluster. A node with a record but no state lost its data: it joins the
// members it recorded, or refuses to start (indexJoinTargets).
func (s *IndexSupervisor) EnsureRQLite(ctx context.Context, nodeID, peerID, httpAdv, raftAdv, joinAddress, extraArgs string) error {
	dataDir := s.CoreRQLiteDir()
	st, err := s.readIndexStart(ctx, dataDir, httpAdv, raftAdv, joinAddress)
	if err != nil {
		return err
	}

	// Which raft id this node starts under. Resolved here because it is a
	// property of the data directory, not of the caller, and getting it wrong
	// in either direction creates a duplicate voter.
	identity, err := s.raftIdentity(dataDir, peerID, raftAdv, st.hasState)
	if err != nil {
		return fmt.Errorf("resolve index raft identity: %w", err)
	}
	if identity.NodeID != "" {
		extraArgs = strings.TrimSpace(extraArgs + " -node-id " + identity.NodeID)
	}
	if identity.AddressChanged(raftAdv) && identity.NonVoter {
		// The rejoin below re-adds this node with the suffrage it asks for.
		extraArgs = strings.TrimSpace(extraArgs + " " + raftNonVoterFlag)
	}
	st.previousAddr = identity.PreviousAddr
	s.logger.Info("Index RQLite raft identity",
		zap.String("node_id", identity.NodeID),
		zap.Bool("stable", identity.Migrated),
		zap.String("raft_adv_addr", raftAdv),
		zap.String("recorded_raft_addr", identity.PreviousAddr))

	joinAddresses, err := indexJoinTargets(st)
	if err != nil {
		return err
	}
	if !st.hasState && !st.recovering && len(joinAddresses) == 0 {
		s.logger.Info("No raft state, no join address and no cluster membership record: bootstrapping a NEW index rqlite cluster (fresh genesis install)",
			zap.String("membership_record", st.recordPath))
	}

	cfg := rqlite.InstanceConfig{
		Namespace:      BlueprintNameIndex,
		NodeID:         nodeID,
		HTTPPort:       IndexRQLiteHTTPPort,
		RaftPort:       IndexRQLiteRaftPort,
		HTTPAdvAddress: httpAdv,
		RaftAdvAddress: raftAdv,
		DataDir:        dataDir,
		ExtraArgs:      extraArgs,
		JoinAddresses:  joinAddresses,
	}

	if err := s.spawnRQLite(ctx, BlueprintNameIndex, nodeID, cfg); err != nil {
		return fmt.Errorf("start orama-namespace-rqlite@index: %w", err)
	}
	return nil
}

// EnsureOlric starts orama-namespace-olric@index using the host olric YAML,
// then stops/disables orama-olric.service so it cannot double-bind the port.
func (s *IndexSupervisor) EnsureOlric(ctx context.Context, nodeID, bindAddr string, peers []string) error {
	hostCfg := filepath.Join(s.oramaDir, "configs", "olric", "config.yaml")
	cfg := olric.InstanceConfig{
		Namespace:      BlueprintNameIndex,
		NodeID:         nodeID,
		HTTPPort:       IndexOlricHTTPPort,
		MemberlistPort: IndexOlricMemberlistPort,
		BindAddr:       bindAddr,
		AdvertiseAddr:  bindAddr,
		PeerAddresses:  peers,
	}
	if err := stopLeftoverUnits("orama-olric.service"); err != nil {
		s.logger.Warn("stop leftover orama-olric", zap.Error(err))
	}
	if _, err := os.Stat(hostCfg); err == nil {
		envVars := map[string]string{
			"OLRIC_SERVER_CONFIG": hostCfg,
			"NODE_ID":             nodeID,
		}
		if err := s.systemdMgr.GenerateEnvFile(BlueprintNameIndex, nodeID, systemd.ServiceTypeOlric, envVars); err != nil {
			return err
		}
		if err := s.systemdMgr.StartService(BlueprintNameIndex, systemd.ServiceTypeOlric); err != nil {
			return fmt.Errorf("start orama-namespace-olric@index: %w", err)
		}
	} else {
		if err := s.spawner.SpawnOlric(ctx, BlueprintNameIndex, nodeID, cfg); err != nil {
			return err
		}
	}
	if err := removeStaleIndexConfigs(s.indexConfigDir(), "olric", nodeID); err != nil {
		return err
	}
	return disableLeftoverUnits("orama-olric.service")
}

// pubsubIdentityDir is where the index pubsub keeps its identity key.
func pubsubIdentityDir(namespaceBase string) string {
	return filepath.Join(namespaceBase, BlueprintNameIndex, "pubsub")
}

// EnsurePubsub starts orama-namespace-pubsub@index on 127.0.0.1:10105.
//
// The identity lives in the namespace's own directory, the only place its
// sandboxed unit may write (ReadWritePaths=.../namespaces/%i). Under data/pubsub
// the unit could not save it and crash-looped on "read-only file system".
func (s *IndexSupervisor) EnsurePubsub(_ context.Context, nodeID string, bootstrap []string) error {
	idDir := pubsubIdentityDir(s.namespaceBase)
	if err := os.MkdirAll(idDir, 0755); err != nil {
		return err
	}
	envVars := map[string]string{
		"PUBSUB_LISTEN":   fmt.Sprintf("127.0.0.1:%d", IndexPubsubPort),
		"IDENTITY_PATH":   filepath.Join(idDir, "identity.key"),
		"BOOTSTRAP_PEERS": strings.Join(bootstrap, ","),
		"NODE_ID":         nodeID,
	}
	if err := s.systemdMgr.GenerateEnvFile(BlueprintNameIndex, nodeID, systemd.ServiceTypePubsub, envVars); err != nil {
		return err
	}
	if err := s.systemdMgr.StartService(BlueprintNameIndex, systemd.ServiceTypePubsub); err != nil {
		return fmt.Errorf("start orama-namespace-pubsub@index: %w", err)
	}
	return nil
}

// EnsureGateway starts orama-namespace-gateway@index on the index gateway port.
// RQLiteDSN is the core DB (the caller's index rqlite endpoint; the spawner
// rejects an empty one); GlobalRQLiteDSN is left empty (this process is the core).
func (s *IndexSupervisor) EnsureGateway(ctx context.Context, cfg gatewayspec.InstanceConfig) error {
	cfg.Namespace = BlueprintNameIndex
	cfg.HTTPPort = IndexGatewayHTTPPort
	cfg.GlobalRQLiteDSN = ""
	if err := s.spawner.SpawnGateway(ctx, BlueprintNameIndex, cfg.NodeID, cfg); err != nil {
		return fmt.Errorf("start orama-namespace-gateway@index: %w", err)
	}
	return removeStaleIndexConfigs(s.indexConfigDir(), "gateway", cfg.NodeID)
}

// indexConfigDir holds the per-node configs of the index services.
func (s *IndexSupervisor) indexConfigDir() string {
	return filepath.Join(s.namespaceBase, BlueprintNameIndex, "configs")
}

// removeStaleIndexConfigs deletes every <service>-*.yaml in configDir except
// the one for nodeID.
//
// Those files are named after the node's id, and that id changed from
// node.id (the same on every nameserver) to the peer id. A file left under the
// old name is read by nothing — the index runs one instance of each service
// per host — and the gateway's embeds the secrets encryption key.
func removeStaleIndexConfigs(configDir, service, nodeID string) error {
	keep := fmt.Sprintf("%s-%s.yaml", service, nodeID)
	stale, err := filepath.Glob(filepath.Join(configDir, service+"-*.yaml"))
	if err != nil {
		return fmt.Errorf("list index %s configs in %s: %w", service, configDir, err)
	}
	for _, path := range stale {
		if filepath.Base(path) == keep {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove stale index %s config %s: %w", service, path, err)
		}
	}
	return nil
}

// unitActive asks systemd whether unit is active. It is a query and needs no
// privilege; it used to go through sudo, which the orama user was never
// granted for is-active, so it reported every unit inactive.
func unitActive(unit string) bool {
	return exec.Command("systemctl", "is-active", "--quiet", unit).Run() == nil
}

// disableLeftoverUnits removes boot enablement without stopping the process.
// Used for WireGuard so we never bounce wg0 (that drops mesh peers).
func disableLeftoverUnits(units ...string) error {
	var first error
	for _, unit := range units {
		cmd := systemd.Systemctl("disable", unit)
		if out, err := cmd.CombinedOutput(); err != nil {
			msg := string(out)
			if strings.Contains(msg, "No such file") || strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist") {
				continue
			}
			if first == nil {
				first = fmt.Errorf("systemctl disable %s: %w (%s)", unit, err, msg)
			}
		}
	}
	return first
}

// stopLeftoverUnits stops pre-factory host units so @index can bind the same ports.
// Do not use this on wg-quick@wg0 — that runs wg-quick down.
func stopLeftoverUnits(units ...string) error {
	var first error
	for _, unit := range units {
		cmd := systemd.Systemctl("stop", unit)
		if out, err := cmd.CombinedOutput(); err != nil {
			msg := string(out)
			if strings.Contains(msg, "not loaded") || strings.Contains(msg, "not found") || strings.Contains(msg, "inactive") || strings.Contains(msg, "does not exist") {
				continue
			}
			if first == nil {
				first = fmt.Errorf("systemctl stop %s: %w (%s)", unit, err, msg)
			}
		}
	}
	return first
}

func (s *IndexSupervisor) writeEnvAndStart(nodeID string, st systemd.ServiceType, envVars map[string]string) error {
	if err := s.systemdMgr.GenerateEnvFile(BlueprintNameIndex, nodeID, st, envVars); err != nil {
		return err
	}
	if err := s.systemdMgr.StartService(BlueprintNameIndex, st); err != nil {
		return fmt.Errorf("start orama-namespace-%s@index: %w", st, err)
	}
	return nil
}
