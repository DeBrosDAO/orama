package production

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FilesystemProvisioner manages directory creation and permissions
type FilesystemProvisioner struct {
	oramaHome string
	oramaDir  string
	logWriter interface{} // Can be io.Writer for logging
}

// NewFilesystemProvisioner creates a new provisioner
func NewFilesystemProvisioner(oramaHome string) *FilesystemProvisioner {
	return &FilesystemProvisioner{
		oramaHome: oramaHome,
		oramaDir:  filepath.Join(oramaHome, ".orama"),
	}
}

// EnsureDirectoryStructure creates all required directories (unified structure)
func (fp *FilesystemProvisioner) EnsureDirectoryStructure() error {
	// All directories needed for unified node structure
	dirs := []string{
		fp.oramaDir,
		filepath.Join(fp.oramaDir, "configs"),
		filepath.Join(fp.oramaDir, "secrets"),
		filepath.Join(fp.oramaDir, "data"),
		filepath.Join(fp.oramaDir, "data", "ipfs", "repo"),
		filepath.Join(fp.oramaDir, "data", "ipfs-cluster"),
		filepath.Join(fp.oramaDir, "data", "rqlite"),
		filepath.Join(fp.oramaDir, "data", "vault"),
		filepath.Join(fp.oramaDir, "logs"),
		filepath.Join(fp.oramaDir, "tls-cache"),
		filepath.Join(fp.oramaDir, "backups"),
		filepath.Join(fp.oramaHome, "bin"),
		filepath.Join(fp.oramaHome, "src"),
		filepath.Join(fp.oramaHome, ".npm"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	if err := os.Chmod(filepath.Join(fp.oramaDir, "secrets"), 0700); err != nil {
		return fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Remove any stray cluster-secret file from root .orama directory
	// The correct location is .orama/secrets/cluster-secret
	strayClusterSecret := filepath.Join(fp.oramaDir, "cluster-secret")
	if _, err := os.Stat(strayClusterSecret); err == nil {
		if err := os.Remove(strayClusterSecret); err != nil {
			return fmt.Errorf("failed to remove stray cluster-secret file: %w", err)
		}
	}

	// Create log files with correct permissions so systemd can write to them
	logsDir := filepath.Join(fp.oramaDir, "logs")
	logFiles := []string{
		"olric.log",
		"gateway.log",
		"ipfs.log",
		"ipfs-cluster.log",
		"node.log",
		"vault.log",
		"anyone-client.log",
	}

	for _, logFile := range logFiles {
		logPath := filepath.Join(logsDir, logFile)
		// Create empty file if it doesn't exist
		if _, err := os.Stat(logPath); os.IsNotExist(err) {
			if err := os.WriteFile(logPath, []byte{}, 0644); err != nil {
				return fmt.Errorf("failed to create log file %s: %w", logPath, err)
			}
		}
	}

	return nil
}

// EnsureOramaUser creates the 'orama' system user and group for running services.
// Sets ownership of the orama data directory to the new user.
func (fp *FilesystemProvisioner) EnsureOramaUser() error {
	// Check if user already exists; create if not
	if err := exec.Command("id", "orama").Run(); err != nil {
		cmd := exec.Command("useradd", "--system", "--no-create-home",
			"--home-dir", fp.oramaHome, "--shell", "/usr/sbin/nologin", "orama")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to create orama user: %w\n%s", err, string(output))
		}

		// Set ownership of orama directories (only on first create)
		chown := exec.Command("chown", "-R", "orama:orama", fp.oramaDir)
		if output, err := chown.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to chown %s: %w\n%s", fp.oramaDir, err, string(output))
		}

		binDir := filepath.Join(fp.oramaHome, "bin")
		if _, err := os.Stat(binDir); err == nil {
			chown = exec.Command("chown", "-R", "orama:orama", binDir)
			if output, err := chown.CombinedOutput(); err != nil {
				return fmt.Errorf("failed to chown %s: %w\n%s", binDir, err, string(output))
			}
		}
	}

	// Always ensure the sudoers rule is up-to-date (handles upgrades too).
	// Resolve binary paths to avoid hardcoding /bin vs /usr/bin vs /usr/sbin.
	systemctlPath, err := exec.LookPath("systemctl")
	if err != nil {
		systemctlPath = "/bin/systemctl" // fallback
	}
	ufwPath, err := exec.LookPath("ufw")
	if err != nil {
		ufwPath = "/usr/sbin/ufw" // fallback
	}

	if err := writeSudoersFile("/etc/sudoers.d/orama-namespaces", oramaSudoersRule(systemctlPath, ufwPath)); err != nil {
		return err
	}

	return nil
}

// oramaSudoersRule returns the /etc/sudoers.d/orama-namespaces content granting
// the unprivileged orama user NOPASSWD access to exactly the systemctl and ufw
// commands it needs at runtime: namespace/deployment service management, and
// opening per-namespace TURN relay firewall ports when WebRTC is enabled
// (FirewallProvisioner.AddWebRTCRules runs `ufw`, which needs root — without
// these ufw entries TURN ports stayed firewalled after `webrtc enable`).
func oramaSudoersRule(systemctlPath, ufwPath string) string {
	return fmt.Sprintf(
		"orama ALL=(root) NOPASSWD: %[1]s start orama-namespace-*, %[1]s stop orama-namespace-*, %[1]s enable orama-namespace-*, %[1]s disable orama-namespace-*, %[1]s restart orama-namespace-*, %[1]s start orama-deploy-*, %[1]s stop orama-deploy-*, %[1]s enable orama-deploy-*, %[1]s disable orama-deploy-*, %[1]s restart orama-deploy-*, %[1]s daemon-reload, %[2]s allow *, %[2]s delete allow *, %[2]s reload, %[2]s status, %[2]s status verbose\n",
		systemctlPath, ufwPath,
	)
}

// writeSudoersFile validates the rule with `visudo -c` before atomically
// installing it (mode 0440). A syntactically broken drop-in is never written,
// so a future edit to oramaSudoersRule can't silently corrupt sudo. The temp
// file is created in the target dir (same filesystem, atomic rename) with a
// leading dot so sudo's includedir ignores it even if cleanup is skipped.
func writeSudoersFile(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".orama-sudoers-*")
	if err != nil {
		return fmt.Errorf("create temp sudoers file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp sudoers file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp sudoers file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0440); err != nil {
		return fmt.Errorf("chmod temp sudoers file: %w", err)
	}

	// Validate syntax before installing. visudo ships with sudo; only skip the
	// check if it is genuinely absent (e.g. a minimal build environment).
	if visudo, lookErr := exec.LookPath("visudo"); lookErr == nil {
		if out, err := exec.Command(visudo, "-c", "-f", tmpPath).CombinedOutput(); err != nil {
			return fmt.Errorf("sudoers rule failed visudo validation: %w\n%s", err, string(out))
		}
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install sudoers file %s: %w", path, err)
	}
	return nil
}

// StateDetector checks for existing production state
type StateDetector struct {
	oramaDir string
}

// NewStateDetector creates a state detector
func NewStateDetector(oramaDir string) *StateDetector {
	return &StateDetector{
		oramaDir: oramaDir,
	}
}

// IsConfigured checks if basic configs exist
func (sd *StateDetector) IsConfigured() bool {
	nodeConfig := filepath.Join(sd.oramaDir, "configs", "node.yaml")
	gatewayConfig := filepath.Join(sd.oramaDir, "configs", "gateway.yaml")
	_, err1 := os.Stat(nodeConfig)
	_, err2 := os.Stat(gatewayConfig)
	return err1 == nil || err2 == nil
}

// HasSecrets checks if cluster secret and swarm key exist
func (sd *StateDetector) HasSecrets() bool {
	clusterSecret := filepath.Join(sd.oramaDir, "secrets", "cluster-secret")
	swarmKey := filepath.Join(sd.oramaDir, "secrets", "swarm.key")
	_, err1 := os.Stat(clusterSecret)
	_, err2 := os.Stat(swarmKey)
	return err1 == nil && err2 == nil
}

// HasIPFSData checks if IPFS repo is initialized (unified path)
func (sd *StateDetector) HasIPFSData() bool {
	// Check unified path first
	ipfsRepoPath := filepath.Join(sd.oramaDir, "data", "ipfs", "repo", "config")
	if _, err := os.Stat(ipfsRepoPath); err == nil {
		return true
	}
	// Fallback: check legacy bootstrap path for migration
	legacyPath := filepath.Join(sd.oramaDir, "data", "bootstrap", "ipfs", "repo", "config")
	_, err := os.Stat(legacyPath)
	return err == nil
}

// HasRQLiteData checks if RQLite data exists (unified path)
func (sd *StateDetector) HasRQLiteData() bool {
	// Check unified path first
	rqliteDataPath := filepath.Join(sd.oramaDir, "data", "rqlite")
	if info, err := os.Stat(rqliteDataPath); err == nil && info.IsDir() {
		return true
	}
	// Fallback: check legacy bootstrap path for migration
	legacyPath := filepath.Join(sd.oramaDir, "data", "bootstrap", "rqlite")
	info, err := os.Stat(legacyPath)
	return err == nil && info.IsDir()
}

// CheckBinaryInstallation checks if required binaries are in PATH
func (sd *StateDetector) CheckBinaryInstallation() error {
	binaries := []string{"ipfs", "ipfs-cluster-service", "rqlited", "olric-server"}
	var missing []string

	for _, bin := range binaries {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing binaries: %s", strings.Join(missing, ", "))
	}

	return nil
}
