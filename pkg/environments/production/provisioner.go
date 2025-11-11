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
	debrosHome string
	debrosDir  string
	logWriter  interface{} // Can be io.Writer for logging
}

// NewFilesystemProvisioner creates a new provisioner
func NewFilesystemProvisioner(debrosHome string) *FilesystemProvisioner {
	return &FilesystemProvisioner{
		debrosHome: debrosHome,
		debrosDir:  filepath.Join(debrosHome, ".debros"),
	}
}

// EnsureDirectoryStructure creates all required directories
func (fp *FilesystemProvisioner) EnsureDirectoryStructure() error {
	dirs := []string{
		fp.debrosDir,
		filepath.Join(fp.debrosDir, "configs"),
		filepath.Join(fp.debrosDir, "secrets"),
		filepath.Join(fp.debrosDir, "data"),
		filepath.Join(fp.debrosDir, "data", "bootstrap", "ipfs", "repo"),
		filepath.Join(fp.debrosDir, "data", "bootstrap", "ipfs-cluster"),
		filepath.Join(fp.debrosDir, "data", "bootstrap", "rqlite"),
		filepath.Join(fp.debrosDir, "data", "node", "ipfs", "repo"),
		filepath.Join(fp.debrosDir, "data", "node", "ipfs-cluster"),
		filepath.Join(fp.debrosDir, "data", "node", "rqlite"),
		filepath.Join(fp.debrosDir, "logs"),
		filepath.Join(fp.debrosDir, "tls-cache"),
		filepath.Join(fp.debrosDir, "backups"),
		filepath.Join(fp.debrosHome, "bin"),
		filepath.Join(fp.debrosHome, "src"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Create log files with correct permissions so systemd can write to them
	logsDir := filepath.Join(fp.debrosDir, "logs")
	logFiles := []string{
		"olric.log",
		"gateway.log",
		"ipfs-bootstrap.log",
		"ipfs-cluster-bootstrap.log",
		"rqlite-bootstrap.log",
		"node-bootstrap.log",
		"ipfs-node.log",
		"ipfs-cluster-node.log",
		"rqlite-node.log",
		"node-node.log",
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

// FixOwnership changes ownership of .debros directory to debros user
func (fp *FilesystemProvisioner) FixOwnership() error {
	// Fix entire .debros directory recursively (includes all data, configs, logs, etc.)
	cmd := exec.Command("chown", "-R", "debros:debros", fp.debrosDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set ownership for %s: %w\nOutput: %s", fp.debrosDir, err, string(output))
	}

	// Also fix home directory ownership
	cmd = exec.Command("chown", "debros:debros", fp.debrosHome)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set ownership for %s: %w\nOutput: %s", fp.debrosHome, err, string(output))
	}

	// Fix bin directory
	binDir := filepath.Join(fp.debrosHome, "bin")
	cmd = exec.Command("chown", "-R", "debros:debros", binDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to set ownership for %s: %w\nOutput: %s", binDir, err, string(output))
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

	sudoersRule := fmt.Sprintf("%s ALL=(debros) NOPASSWD: ALL\n", invokerUser)
	sudoersFile := "/etc/sudoers.d/debros-access"

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

// StateDetector checks for existing production state
type StateDetector struct {
	debrosDir string
}

// NewStateDetector creates a state detector
func NewStateDetector(debrosDir string) *StateDetector {
	return &StateDetector{
		debrosDir: debrosDir,
	}
}

// IsConfigured checks if basic configs exist
func (sd *StateDetector) IsConfigured() bool {
	nodeConfig := filepath.Join(sd.debrosDir, "configs", "node.yaml")
	gatewayConfig := filepath.Join(sd.debrosDir, "configs", "gateway.yaml")
	_, err1 := os.Stat(nodeConfig)
	_, err2 := os.Stat(gatewayConfig)
	return err1 == nil || err2 == nil
}

// HasSecrets checks if cluster secret and swarm key exist
func (sd *StateDetector) HasSecrets() bool {
	clusterSecret := filepath.Join(sd.debrosDir, "secrets", "cluster-secret")
	swarmKey := filepath.Join(sd.debrosDir, "secrets", "swarm.key")
	_, err1 := os.Stat(clusterSecret)
	_, err2 := os.Stat(swarmKey)
	return err1 == nil && err2 == nil
}

// HasIPFSData checks if IPFS repo is initialized
func (sd *StateDetector) HasIPFSData() bool {
	ipfsRepoPath := filepath.Join(sd.debrosDir, "data", "bootstrap", "ipfs", "repo", "config")
	_, err := os.Stat(ipfsRepoPath)
	return err == nil
}

// HasRQLiteData checks if RQLite data exists
func (sd *StateDetector) HasRQLiteData() bool {
	rqliteDataPath := filepath.Join(sd.debrosDir, "data", "bootstrap", "rqlite")
	info, err := os.Stat(rqliteDataPath)
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
