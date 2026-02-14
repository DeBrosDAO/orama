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

// FixOwnership changes ownership of .orama directory to orama user
func (fp *FilesystemProvisioner) FixOwnership() error {
	// Fix entire .orama directory recursively (includes all data, configs, logs, etc.)
	cmd := exec.Command("chown", "-R", "orama:orama", fp.oramaDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set ownership for %s: %w\nOutput: %s", fp.oramaDir, err, string(output))
	}

	// Also fix home directory ownership
	cmd = exec.Command("chown", "orama:orama", fp.oramaHome)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set ownership for %s: %w\nOutput: %s", fp.oramaHome, err, string(output))
	}

	// Fix bin directory
	binDir := filepath.Join(fp.oramaHome, "bin")
	cmd = exec.Command("chown", "-R", "orama:orama", binDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set ownership for %s: %w\nOutput: %s", binDir, err, string(output))
	}

	// Fix npm cache directory
	npmDir := filepath.Join(fp.oramaHome, ".npm")
	cmd = exec.Command("chown", "-R", "orama:orama", npmDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set ownership for %s: %w\nOutput: %s", npmDir, err, string(output))
	}

	return nil
}

// UserProvisioner manages system user creation and sudoers setup
type UserProvisioner struct {
	username string
	home     string
	shell    string
}

// NewUserProvisioner creates a new user provisioner
func NewUserProvisioner(username, home, shell string) *UserProvisioner {
	if shell == "" {
		shell = "/bin/bash"
	}
	return &UserProvisioner{
		username: username,
		home:     home,
		shell:    shell,
	}
}

// UserExists checks if the system user exists
func (up *UserProvisioner) UserExists() bool {
	cmd := exec.Command("id", up.username)
	return cmd.Run() == nil
}

// CreateUser creates the system user
func (up *UserProvisioner) CreateUser() error {
	if up.UserExists() {
		return nil // User already exists
	}

	cmd := exec.Command("useradd", "-r", "-m", "-s", up.shell, "-d", up.home, up.username)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create user %s: %w", up.username, err)
	}

	return nil
}

// SetupSudoersAccess creates sudoers rule for the invoking user
func (up *UserProvisioner) SetupSudoersAccess(invokerUser string) error {
	if invokerUser == "" {
		return nil // Skip if no invoker
	}

	sudoersRule := fmt.Sprintf("%s ALL=(orama) NOPASSWD: ALL\n", invokerUser)
	sudoersFile := "/etc/sudoers.d/orama-access"

	// Check if rule already exists
	if existing, err := os.ReadFile(sudoersFile); err == nil {
		if strings.Contains(string(existing), invokerUser) {
			return nil // Rule already set
		}
	}

	// Write sudoers rule
	if err := os.WriteFile(sudoersFile, []byte(sudoersRule), 0440); err != nil {
		return fmt.Errorf("failed to create sudoers rule: %w", err)
	}

	// Validate sudoers file
	cmd := exec.Command("visudo", "-c", "-f", sudoersFile)
	if err := cmd.Run(); err != nil {
		os.Remove(sudoersFile) // Clean up on validation failure
		return fmt.Errorf("sudoers rule validation failed: %w", err)
	}

	return nil
}

// SetupDeploymentSudoers configures the orama user with permissions needed for
// managing user deployments via systemd services.
func (up *UserProvisioner) SetupDeploymentSudoers() error {
	sudoersFile := "/etc/sudoers.d/orama-deployments"

	// Check if already configured
	if _, err := os.Stat(sudoersFile); err == nil {
		return nil // Already configured
	}

	sudoersContent := `# Orama Network - Deployment Management Permissions
# Allows orama user to manage systemd services for user deployments

# Systemd service management for orama-deploy-* services
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl daemon-reload
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl start orama-deploy-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl stop orama-deploy-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart orama-deploy-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl enable orama-deploy-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl disable orama-deploy-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl status orama-deploy-*

# Service file management (tee to write, rm to remove)
orama ALL=(ALL) NOPASSWD: /usr/bin/tee /etc/systemd/system/orama-deploy-*.service
orama ALL=(ALL) NOPASSWD: /bin/rm -f /etc/systemd/system/orama-deploy-*.service
`

	// Write sudoers rule
	if err := os.WriteFile(sudoersFile, []byte(sudoersContent), 0440); err != nil {
		return fmt.Errorf("failed to create deployment sudoers rule: %w", err)
	}

	// Validate sudoers file
	cmd := exec.Command("visudo", "-c", "-f", sudoersFile)
	if err := cmd.Run(); err != nil {
		os.Remove(sudoersFile) // Clean up on validation failure
		return fmt.Errorf("deployment sudoers rule validation failed: %w", err)
	}

	return nil
}

// SetupNamespaceSudoers configures the orama user with permissions needed for
// managing namespace cluster services via systemd.
func (up *UserProvisioner) SetupNamespaceSudoers() error {
	sudoersFile := "/etc/sudoers.d/orama-namespaces"

	// Check if already configured
	if _, err := os.Stat(sudoersFile); err == nil {
		return nil // Already configured
	}

	sudoersContent := `# Orama Network - Namespace Cluster Management Permissions
# Allows orama user to manage systemd services for namespace clusters

# Systemd service management for orama-namespace-* services
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl daemon-reload
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl start orama-namespace-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl stop orama-namespace-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart orama-namespace-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl enable orama-namespace-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl disable orama-namespace-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl status orama-namespace-*
orama ALL=(ALL) NOPASSWD: /usr/bin/systemctl is-active orama-namespace-*

# Service file management (tee to write, rm to remove)
orama ALL=(ALL) NOPASSWD: /usr/bin/tee /etc/systemd/system/orama-namespace-*.service
orama ALL=(ALL) NOPASSWD: /bin/rm -f /etc/systemd/system/orama-namespace-*.service

# Environment file management for namespace services
orama ALL=(ALL) NOPASSWD: /usr/bin/tee /home/orama/.orama/namespace/*/env/*
orama ALL=(ALL) NOPASSWD: /usr/bin/mkdir -p /home/orama/.orama/namespace/*/env
`

	// Write sudoers rule
	if err := os.WriteFile(sudoersFile, []byte(sudoersContent), 0440); err != nil {
		return fmt.Errorf("failed to create namespace sudoers rule: %w", err)
	}

	// Validate sudoers file
	cmd := exec.Command("visudo", "-c", "-f", sudoersFile)
	if err := cmd.Run(); err != nil {
		os.Remove(sudoersFile) // Clean up on validation failure
		return fmt.Errorf("namespace sudoers rule validation failed: %w", err)
	}

	return nil
}

// SetupWireGuardSudoers configures the orama user with permissions to manage WireGuard
func (up *UserProvisioner) SetupWireGuardSudoers() error {
	sudoersFile := "/etc/sudoers.d/orama-wireguard"

	sudoersContent := `# Orama Network - WireGuard Management Permissions
# Allows orama user to manage WireGuard peers

orama ALL=(ALL) NOPASSWD: /usr/bin/wg set wg0 *
orama ALL=(ALL) NOPASSWD: /usr/bin/wg show wg0
orama ALL=(ALL) NOPASSWD: /usr/bin/wg showconf wg0
orama ALL=(ALL) NOPASSWD: /usr/bin/tee /etc/wireguard/wg0.conf
`

	// Write sudoers rule (always overwrite to ensure latest)
	if err := os.WriteFile(sudoersFile, []byte(sudoersContent), 0440); err != nil {
		return fmt.Errorf("failed to create wireguard sudoers rule: %w", err)
	}

	// Validate sudoers file
	cmd := exec.Command("visudo", "-c", "-f", sudoersFile)
	if err := cmd.Run(); err != nil {
		os.Remove(sudoersFile)
		return fmt.Errorf("wireguard sudoers rule validation failed: %w", err)
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
