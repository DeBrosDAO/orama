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
	cmd := exec.Command("wget", "-q", "-4", url, "-O", dest)
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
