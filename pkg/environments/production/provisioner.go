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
