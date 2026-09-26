package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/constants"
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

// gatewayTreeMode is the mode of the trees only the gateway reads and writes.
const gatewayTreeMode = 0o700

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
		// The gateways' writable trees. Their unit lists each in
		// ReadWritePaths and leaves the rest of data/ read-only, so they must
		// exist before a gateway starts: it can no longer create them.
		constants.NamespacesDir(fp.oramaDir),
		filepath.Dir(constants.HostTURNConfigPath(fp.oramaDir)),
		filepath.Join(fp.oramaDir, "logs"),
		filepath.Join(fp.oramaDir, "tls-cache"),
		filepath.Join(fp.oramaDir, "backups"),
		filepath.Join(fp.oramaHome, "bin"),
		filepath.Join(fp.oramaHome, ".npm"),
	}

	root := OramaRoot(fp.oramaDir)
	for _, dir := range dirs {
		if err := root.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	// The deployment and SQLite trees are the gateway's alone (it narrows
	// data/deployments to 0700 itself on every claim); created here because
	// the gateway's unit may write inside them but not create them.
	for _, dir := range []string{constants.DeploymentsBaseDir(fp.oramaDir), constants.SQLiteBaseDir(fp.oramaDir)} {
		if err := root.MkdirAll(dir, gatewayTreeMode); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	if err := root.Chmod(filepath.Join(fp.oramaDir, "secrets"), 0700); err != nil {
		return fmt.Errorf("failed to set secrets directory permissions: %w", err)
	}

	// Remove any stray cluster-secret file from root .orama directory
	// The correct location is .orama/secrets/cluster-secret
	strayClusterSecret := filepath.Join(fp.oramaDir, "cluster-secret")
	if err := root.Remove(strayClusterSecret); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to remove stray cluster-secret file: %w", err)
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

		if err := lockOramaBinDir(filepath.Join(fp.oramaHome, "bin")); err != nil {
			return err
		}
	}

	return nil
}

// lockOramaBinDir makes bin/ root:orama 0750 so the orama user can execute
// binaries but cannot replace them (bugboard #242).
func lockOramaBinDir(binDir string) error {
	if _, err := os.Stat(binDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", binDir, err)
	}
	if output, err := exec.Command("chown", "root:orama", binDir).CombinedOutput(); err != nil {
		return fmt.Errorf("chown %s: %w\n%s", binDir, err, string(output))
	}
	if err := os.Chmod(binDir, 0o750); err != nil {
		return fmt.Errorf("chmod %s: %w", binDir, err)
	}
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", binDir, err)
	}
	for _, e := range entries {
		p := filepath.Join(binDir, e.Name())
		if output, err := exec.Command("chown", "root:orama", p).CombinedOutput(); err != nil {
			return fmt.Errorf("chown %s: %w\n%s", p, err, string(output))
		}
		if err := os.Chmod(p, 0o750); err != nil {
			return fmt.Errorf("chmod %s: %w", p, err)
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
