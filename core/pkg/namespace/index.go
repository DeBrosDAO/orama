package namespace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// IsIndexGateway reports whether this process is the core/index gateway.
func IsIndexGateway(cfg *gateway.Config) bool {
	return cfg != nil && cfg.ClientNamespace == BlueprintNameIndex
}

// ErrEmptyAdopt is returned when @index rqlite would start with no raft.db.
// That would create a new cluster and wipe the registry.
var ErrEmptyAdopt = &ClusterError{Message: "adopt refused: no raft.db in index data dir (refusing to create a new cluster)"}

// HasExistingRaft reports whether rqliteDataDir already holds a real raft.db.
func HasExistingRaft(rqliteDataDir string) bool {
	info, err := os.Stat(filepath.Join(rqliteDataDir, "raft.db"))
	if err != nil {
		return false
	}
	return info.Size() > 1024
}

// RefuseEmptyAdopt fails if pointing @index rqlite at dataDir would start a new raft.
func RefuseEmptyAdopt(rqliteDataDir string) error {
	if !HasExistingRaft(rqliteDataDir) {
		return ErrEmptyAdopt
	}
	return nil
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
}

// NewIndexSupervisor builds a supervisor. oramaDir is ~/.orama (parent of data/).
func NewIndexSupervisor(oramaDir string, logger *zap.Logger) *IndexSupervisor {
	dataDir := filepath.Join(oramaDir, "data")
	namespaceBase := filepath.Join(dataDir, "namespaces")
	if logger == nil {
		logger = zap.NewNop()
	}
	return &IndexSupervisor{
		oramaDir:      oramaDir,
		dataDir:       dataDir,
		namespaceBase: namespaceBase,
		spawner:       NewSystemdSpawner(namespaceBase, filepath.Join(oramaDir, "secrets", "cluster-secret"), logger),
		systemdMgr:    systemd.NewManager(namespaceBase, logger),
		logger:        logger.With(zap.String("component", "index-supervisor")),
	}
}

// CoreRQLiteDir is the live raft directory (~/.orama/data/rqlite). Never namespaces/index/rqlite.
func (s *IndexSupervisor) CoreRQLiteDir() string {
	return filepath.Join(s.dataDir, "rqlite")
}

// EnsureRQLite writes @index env (DATA_DIR = existing core raft) and starts the unit.
// If requireExisting is true, an empty dir is refused.
func (s *IndexSupervisor) EnsureRQLite(ctx context.Context, nodeID, httpAdv, raftAdv, joinAddress, extraArgs string, requireExisting bool) error {
	dataDir := s.CoreRQLiteDir()
	if requireExisting {
		if err := RefuseEmptyAdopt(dataDir); err != nil {
			return err
		}
	}

	joinArgs := ""
	if !HasExistingRaft(dataDir) && joinAddress != "" {
		joinArgs = "-join " + joinAddress
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
	}
	if joinArgs != "" {
		cfg.JoinAddresses = []string{joinAddress}
	}

	if err := s.spawner.SpawnRQLite(ctx, BlueprintNameIndex, nodeID, cfg); err != nil {
		return fmt.Errorf("start orama-namespace-rqlite@index: %w", err)
	}
	return nil
}

// EnsureOlric starts orama-namespace-olric@index using the host olric YAML,
// then stops/disables orama-olric.service so it cannot double-bind :3320.
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
	return disableLeftoverUnits("orama-olric.service")
}

// EnsurePubsub starts orama-namespace-pubsub@index on 127.0.0.1:10105.
func (s *IndexSupervisor) EnsurePubsub(_ context.Context, nodeID string, bootstrap []string) error {
	idDir := filepath.Join(s.dataDir, "pubsub")
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

// EnsureGateway starts orama-namespace-gateway@index on :6001.
// RQLiteDSN is the core DB; GlobalRQLiteDSN is left empty (this process is the core).
func (s *IndexSupervisor) EnsureGateway(ctx context.Context, cfg gateway.InstanceConfig) error {
	cfg.Namespace = BlueprintNameIndex
	cfg.HTTPPort = IndexGatewayHTTPPort
	if cfg.RQLiteDSN == "" {
		cfg.RQLiteDSN = fmt.Sprintf("http://localhost:%d", IndexRQLiteHTTPPort)
	}
	cfg.GlobalRQLiteDSN = ""
	if err := s.spawner.SpawnGateway(ctx, BlueprintNameIndex, cfg.NodeID, cfg); err != nil {
		return fmt.Errorf("start orama-namespace-gateway@index: %w", err)
	}
	return nil
}

func systemctlCmd(args ...string) *exec.Cmd {
	if os.Getuid() == 0 {
		return exec.Command("systemctl", args...)
	}
	return exec.Command("sudo", append([]string{"systemctl"}, args...)...)
}

func unitActive(unit string) bool {
	return systemctlCmd("is-active", "--quiet", unit).Run() == nil
}

// disableLeftoverUnits removes boot enablement without stopping the process.
// Used for WireGuard so we never bounce wg0 (that drops mesh peers).
func disableLeftoverUnits(units ...string) error {
	var first error
	for _, unit := range units {
		cmd := systemctlCmd("disable", unit)
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
		cmd := systemctlCmd("stop", unit)
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
