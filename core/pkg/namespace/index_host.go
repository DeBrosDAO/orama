package namespace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

func (s *IndexSupervisor) adoptReplace(nodeID string, st systemd.ServiceType, leftover []string, envVars map[string]string) error {
	if err := stopLeftoverUnits(leftover...); err != nil {
		s.logger.Warn("stop leftover host unit", zap.Strings("units", leftover), zap.Error(err))
	}
	if err := s.writeEnvAndStart(nodeID, st, envVars); err != nil {
		return err
	}
	return disableLeftoverUnits(leftover...)
}

// EnsureWireGuard brings up the existing /etc/wireguard/wg0.conf via
// orama-namespace-wireguard@index. It never writes a new conf. Leftover
// wg-quick@wg0 is disabled without --now so the interface is not bounced.
//
// It does not stat the conf first: /etc/wireguard is root's (wg-quick runs the
// conf's PostUp lines as root) and this process is the orama user, so the stat
// failed with permission denied on every node and WireGuard never came up. A
// missing conf fails the unit start, whose error names the unit to inspect.
func (s *IndexSupervisor) EnsureWireGuard(nodeID string) error {
	if err := disableLeftoverUnits(systemd.LeftoverWireGuardUnit); err != nil {
		s.logger.Warn("disable leftover wg-quick@wg0", zap.Error(err))
	}
	if unitActive("orama-namespace-wireguard@index.service") {
		return nil
	}
	if err := s.systemdMgr.StartService(BlueprintNameIndex, systemd.ServiceTypeWireGuard); err != nil {
		return fmt.Errorf("start orama-namespace-wireguard@index: %w", err)
	}
	return nil
}

// EnsureIPFS starts orama-namespace-ipfs@index against the existing repo.
func (s *IndexSupervisor) EnsureIPFS(nodeID string) error {
	repo := filepath.Join(s.dataDir, "ipfs", "repo")
	return s.adoptReplace(nodeID, systemd.ServiceTypeIPFS, []string{"orama-ipfs.service"}, map[string]string{
		"IPFS_PATH": repo,
		"NODE_ID":   nodeID,
	})
}

// EnsureIPFSCluster starts orama-namespace-ipfs-cluster@index against the
// existing cluster data dir and secrets/cluster-secret.
//
// A secret that cannot be read, or is empty, is an error. It used to start
// the daemon with CLUSTER_SECRET="" instead: ipfs-cluster then runs a private
// network keyed by nothing, handshakes with no peer, and reports healthy.
func (s *IndexSupervisor) EnsureIPFSCluster(nodeID string) error {
	secret, err := readClusterSecret(filepath.Join(s.oramaDir, "secrets", "cluster-secret"))
	if err != nil {
		return err
	}
	clusterPath := filepath.Join(s.dataDir, "ipfs-cluster")
	return s.adoptReplace(nodeID, systemd.ServiceTypeIPFSCluster, []string{"orama-ipfs-cluster.service"}, map[string]string{
		"IPFS_CLUSTER_PATH": clusterPath,
		"CLUSTER_SECRET":    secret,
		"NODE_ID":           nodeID,
	})
}

// readClusterSecret is the IPFS Cluster shared secret at path. Install writes
// it and the join handshake distributes it, so its absence is not something
// the supervisor can repair.
func readClusterSecret(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the IPFS Cluster secret %s: %w (install writes it; restore it from another node — it is the same value fleet-wide)", path, err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", fmt.Errorf("the IPFS Cluster secret %s is empty; restore it from another node — it is the same value fleet-wide", path)
	}
	return secret, nil
}

// ipfsGCEnv is the GC oneshot's environment: the repo, and the API of the
// running daemon it collects through.
func ipfsGCEnv(repo, nodeID, apiAuth string) map[string]string {
	return map[string]string{
		"IPFS_PATH":     repo,
		"IPFS_API":      fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", IndexIPFSAPIPort),
		"IPFS_API_AUTH": apiAuth,
		"NODE_ID":       nodeID,
	}
}

// EnsureIPFSGC starts the instantiated GC timer (not the oneshot).
func (s *IndexSupervisor) EnsureIPFSGC(nodeID string) error {
	secret, err := readClusterSecret(filepath.Join(s.oramaDir, "secrets", "cluster-secret"))
	if err != nil {
		return err
	}
	token, err := ipfs.KuboAPIToken(secret)
	if err != nil {
		return err
	}
	if err := s.systemdMgr.GenerateEnvFile(BlueprintNameIndex, nodeID, systemd.ServiceTypeIPFSGC,
		ipfsGCEnv(filepath.Join(s.dataDir, "ipfs", "repo"), nodeID, "bearer:"+token)); err != nil {
		return err
	}
	if err := stopLeftoverUnits("orama-ipfs-gc.timer"); err != nil {
		s.logger.Warn("stop leftover ipfs-gc timer", zap.Error(err))
	}
	if err := s.systemdMgr.StartTimer(BlueprintNameIndex, systemd.ServiceTypeIPFSGC); err != nil {
		return err
	}
	return disableLeftoverUnits("orama-ipfs-gc.timer")
}

// EnsureVault starts orama-namespace-vault@index with the existing vault.yaml.
func (s *IndexSupervisor) EnsureVault(nodeID string) error {
	cfg := filepath.Join(s.dataDir, "vault", "vault.yaml")
	if _, err := os.Stat(cfg); err != nil {
		return fmt.Errorf("adopt vault: missing %s: %w", cfg, err)
	}
	return s.adoptReplace(nodeID, systemd.ServiceTypeVault, []string{"orama-vault.service"}, map[string]string{
		"NODE_ID": nodeID,
	})
}

// EnsureCaddy starts orama-namespace-caddy@index using the existing Caddyfile.
func (s *IndexSupervisor) EnsureCaddy(nodeID string) error {
	if _, err := os.Stat("/usr/bin/caddy"); err != nil {
		return fmt.Errorf("adopt caddy: /usr/bin/caddy not installed: %w", err)
	}
	return s.adoptReplace(nodeID, systemd.ServiceTypeCaddy, []string{"caddy.service"}, map[string]string{
		"NODE_ID": nodeID,
	})
}

// EnsureNtfy starts orama-namespace-ntfy@index when the ntfy binary is present.
func (s *IndexSupervisor) EnsureNtfy(nodeID string) error {
	if _, err := os.Stat("/usr/local/bin/ntfy"); err != nil {
		s.logger.Info("ntfy binary not installed; skipping")
		return disableLeftoverUnits("ntfy.service")
	}
	if err := stopLeftoverUnits("ntfy.service"); err != nil {
		s.logger.Warn("stop leftover host unit", zap.String("unit", "ntfy.service"), zap.Error(err))
	}
	if err := s.startWithoutEnv(systemd.ServiceTypeNtfy); err != nil {
		return err
	}
	return disableLeftoverUnits("ntfy.service")
}

// EnsureTor starts orama-namespace-tor@index, the node's client-only Tor
// daemon. Every node has it: install and upgrade write the torrc, so a missing
// one means that phase did not run, and is reported rather than skipped.
func (s *IndexSupervisor) EnsureTor(nodeID string) error {
	if _, err := os.Stat(constants.TorConfigPath); err != nil {
		return fmt.Errorf("tor: missing %s (written by `orama node install`/`upgrade`; re-run the upgrade on this node): %w", constants.TorConfigPath, err)
	}
	return s.startWithoutEnv(systemd.ServiceTypeTor)
}

// startWithoutEnv starts a unit that runs as another user (tor, ntfy) or as
// root (wireguard). Those units read no env file: anything in one would be
// set by the orama user for a process it does not own. orama-privhelper
// refuses to write one for them.
func (s *IndexSupervisor) startWithoutEnv(st systemd.ServiceType) error {
	if err := s.systemdMgr.StartService(BlueprintNameIndex, st); err != nil {
		return fmt.Errorf("start orama-namespace-%s@index: %w", st, err)
	}
	return nil
}

// EnsureSNIRouter starts orama-namespace-sni-router@index when enabled.
// When disabled, leftover orama-sni-router is stopped so Caddy can bind :443.
func (s *IndexSupervisor) EnsureSNIRouter(nodeID string, enabled bool) error {
	leftover := []string{"orama-sni-router.service"}
	if !enabled {
		if err := stopLeftoverUnits(leftover...); err != nil {
			s.logger.Warn("stop leftover sni-router", zap.Error(err))
		}
		if unitActive("orama-namespace-sni-router@index.service") {
			if err := s.systemdMgr.StopService(BlueprintNameIndex, systemd.ServiceTypeSNIRouter); err != nil {
				s.logger.Warn("stop index sni-router", zap.Error(err))
			}
		}
		return disableLeftoverUnits(leftover...)
	}
	bin := filepath.Join(s.oramaDir, "..", "bin", "orama-sni-router")
	if _, err := os.Stat("/opt/orama/bin/orama-sni-router"); err != nil {
		if _, err2 := os.Stat(bin); err2 != nil {
			return fmt.Errorf("sni_router.enabled but orama-sni-router binary missing")
		}
	}
	return s.adoptReplace(nodeID, systemd.ServiceTypeSNIRouter, leftover, map[string]string{
		"NODE_ID": nodeID,
	})
}

// EnsureCoreDNS starts orama-namespace-coredns@nameserver against the existing
// Corefile. Call only on nodes installed with --nameserver, after index rqlite
// is up. Zone data stays in index RQLite dns_records (localhost:<index-rqlite-http>).
func (s *IndexSupervisor) EnsureCoreDNS(nodeID string) error {
	if _, err := os.Stat("/usr/local/bin/coredns"); err != nil {
		return fmt.Errorf("nameserver: /usr/local/bin/coredns not installed: %w", err)
	}
	if _, err := os.Stat("/etc/coredns/Corefile"); err != nil {
		return fmt.Errorf("nameserver: missing /etc/coredns/Corefile: %w", err)
	}
	leftover := []string{systemd.LeftoverNameserverUnit}
	if err := stopLeftoverUnits(leftover...); err != nil {
		s.logger.Warn("stop leftover coredns", zap.Error(err))
	}
	if err := s.systemdMgr.GenerateEnvFile(BlueprintNameNameserver, nodeID, systemd.ServiceTypeCoreDNS, map[string]string{
		"NODE_ID": nodeID,
	}); err != nil {
		return err
	}
	if err := s.systemdMgr.StartService(BlueprintNameNameserver, systemd.ServiceTypeCoreDNS); err != nil {
		return fmt.Errorf("start orama-namespace-coredns@nameserver: %w", err)
	}
	return disableLeftoverUnits(leftover...)
}
