package namespace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// EnsureWireGuard brings up existing /etc/wireguard/wg0.conf via
// orama-namespace-wireguard@index. It never writes a new conf. Leftover
// wg-quick@wg0 is disabled without --now so the interface is not bounced.
func (s *IndexSupervisor) EnsureWireGuard(nodeID string) error {
	if _, err := os.Stat("/etc/wireguard/wg0.conf"); err != nil {
		return fmt.Errorf("adopt wireguard: missing /etc/wireguard/wg0.conf: %w", err)
	}
	if err := disableLeftoverUnits(systemd.LeftoverWireGuardUnit); err != nil {
		s.logger.Warn("disable leftover wg-quick@wg0", zap.Error(err))
	}
	envVars := map[string]string{"NODE_ID": nodeID}
	if err := s.systemdMgr.GenerateEnvFile(BlueprintNameIndex, nodeID, systemd.ServiceTypeWireGuard, envVars); err != nil {
		return err
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
func (s *IndexSupervisor) EnsureIPFSCluster(nodeID string) error {
	secretPath := filepath.Join(s.oramaDir, "secrets", "cluster-secret")
	secret := ""
	if data, err := os.ReadFile(secretPath); err == nil {
		secret = strings.TrimSpace(string(data))
	}
	clusterPath := filepath.Join(s.dataDir, "ipfs-cluster")
	return s.adoptReplace(nodeID, systemd.ServiceTypeIPFSCluster, []string{"orama-ipfs-cluster.service"}, map[string]string{
		"IPFS_CLUSTER_PATH": clusterPath,
		"CLUSTER_SECRET":    secret,
		"NODE_ID":           nodeID,
	})
}

// EnsureIPFSGC starts the instantiated GC timer (not the oneshot).
func (s *IndexSupervisor) EnsureIPFSGC(nodeID string) error {
	repo := filepath.Join(s.dataDir, "ipfs", "repo")
	if err := s.systemdMgr.GenerateEnvFile(BlueprintNameIndex, nodeID, systemd.ServiceTypeIPFSGC, map[string]string{
		"IPFS_PATH": repo,
		"NODE_ID":   nodeID,
	}); err != nil {
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
	return s.adoptReplace(nodeID, systemd.ServiceTypeNtfy, []string{"ntfy.service"}, map[string]string{
		"NODE_ID": nodeID,
	})
}

// EnsureAnyoneClient starts orama-namespace-anyone-client@index when anonrc exists.
func (s *IndexSupervisor) EnsureAnyoneClient(nodeID string) error {
	if _, err := os.Stat("/etc/anon/anonrc"); err != nil {
		s.logger.Info("anyone client not installed; skipping")
		return disableLeftoverUnits("orama-anyone-client.service")
	}
	return s.adoptReplace(nodeID, systemd.ServiceTypeAnyoneClient, []string{"orama-anyone-client.service"}, map[string]string{
		"NODE_ID": nodeID,
	})
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
