package installers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DownloadFile downloads a file from a URL to a destination path
func DownloadFile(url, dest string) error {
	cmd := exec.Command("wget", "-q", url, "-O", dest)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	return nil
}

// ExtractTarball extracts a tarball to a destination directory
func ExtractTarball(tarPath, destDir string) error {
	cmd := exec.Command("tar", "-xzf", tarPath, "-C", destDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	return nil
}

// ResolveBinaryPath finds the fully-qualified path to a required executable
func ResolveBinaryPath(binary string, extraPaths ...string) (string, error) {
	// First try to find in PATH
	if path, err := exec.LookPath(binary); err == nil {
		if abs, err := filepath.Abs(path); err == nil {
			return abs, nil
		}
		return path, nil
	}

	// Then try extra candidate paths
	for _, candidate := range extraPaths {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			if abs, err := filepath.Abs(candidate); err == nil {
				return abs, nil
			}
			return candidate, nil
		}
	}

	// Not found - generate error message
	checked := make([]string, 0, len(extraPaths))
	for _, candidate := range extraPaths {
		if candidate != "" {
			checked = append(checked, candidate)
		}
	}

	if len(checked) == 0 {
		return "", fmt.Errorf("required binary %q not found in path", binary)
	}

	return "", fmt.Errorf("required binary %q not found in path (also checked %s)", binary, strings.Join(checked, ", "))
}

// CreateSystemdService creates a systemd service unit file
func CreateSystemdService(name, content string) error {
	servicePath := filepath.Join("/etc/systemd/system", name)
	if err := os.WriteFile(servicePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}
	return nil
}

// EnableSystemdService enables a systemd service
func EnableSystemdService(name string) error {
	cmd := exec.Command("systemctl", "enable", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to enable service: %w", err)
	}
	return nil
}

// StartSystemdService starts a systemd service
func StartSystemdService(name string) error {
	cmd := exec.Command("systemctl", "start", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}
	return nil
}

// ReloadSystemdDaemon reloads systemd daemon configuration
func ReloadSystemdDaemon() error {
	cmd := exec.Command("systemctl", "daemon-reload")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to reload systemd: %w", err)
	}
	return nil
}

// SetFileOwnership sets ownership of a file or directory
func SetFileOwnership(path, owner string) error {
	cmd := exec.Command("chown", "-R", owner, path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set ownership: %w", err)
	}
	return nil
}

// SetFilePermissions sets permissions on a file or directory
func SetFilePermissions(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}
	return nil
}

// EnsureDirectory creates a directory if it doesn't exist
func EnsureDirectory(path string, mode os.FileMode) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}
