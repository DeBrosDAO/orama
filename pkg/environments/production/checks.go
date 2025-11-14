package production

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// OSInfo contains detected operating system information
type OSInfo struct {
	ID      string // ubuntu, debian, etc.
	Version string // 22.04, 24.04, 12, etc.
	Name    string // Full name: "ubuntu 24.04"
}

// PrivilegeChecker validates root access and user context
type PrivilegeChecker struct{}

// CheckRoot verifies the process is running as root
func (pc *PrivilegeChecker) CheckRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("this command must be run as root (use sudo)")
	}
	return nil
}

// CheckLinuxOS verifies the process is running on Linux
func (pc *PrivilegeChecker) CheckLinuxOS() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("production setup is only supported on Linux (detected: %s)", runtime.GOOS)
	}
	return nil
}

// OSDetector detects the Linux distribution
type OSDetector struct{}

// Detect returns information about the detected OS
func (od *OSDetector) Detect() (*OSInfo, error) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return nil, fmt.Errorf("cannot detect operating system: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var id, version string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID=") {
			id = strings.Trim(strings.TrimPrefix(line, "ID="), "\"")
		}
		if strings.HasPrefix(line, "VERSION_ID=") {
			version = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
		}
	}

	if id == "" {
		return nil, fmt.Errorf("could not detect OS ID from /etc/os-release")
	}

	name := id
	if version != "" {
		name = fmt.Sprintf("%s %s", id, version)
	}

	return &OSInfo{
		ID:      id,
		Version: version,
		Name:    name,
	}, nil
}

// IsSupportedOS checks if the OS is supported for production deployment
func (od *OSDetector) IsSupportedOS(info *OSInfo) bool {
	supported := map[string][]string{
		"ubuntu": {"22.04", "24.04", "25.04"},
		"debian": {"12"},
	}

	versions, ok := supported[info.ID]
	if !ok {
		return false
	}

	for _, v := range versions {
		if info.Version == v {
			return true
		}
	}

	return false
}

// ArchitectureDetector detects the system architecture
type ArchitectureDetector struct{}

// Detect returns the detected architecture as a string usable for downloads
func (ad *ArchitectureDetector) Detect() (string, error) {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		return "amd64", nil
	case "arm64":
		return "arm64", nil
	case "arm":
		return "arm", nil
	default:
		return "", fmt.Errorf("unsupported architecture: %s", arch)
	}
}

// DependencyChecker validates external tool availability
type DependencyChecker struct {
	skipOptional bool
}

// NewDependencyChecker creates a new checker
func NewDependencyChecker(skipOptional bool) *DependencyChecker {
	return &DependencyChecker{
		skipOptional: skipOptional,
	}
}

// Dependency represents an external binary dependency
type Dependency struct {
	Name        string
	Command     string
	Optional    bool
	InstallHint string
}

// CheckAll validates all required dependencies
func (dc *DependencyChecker) CheckAll() ([]Dependency, error) {
	dependencies := []Dependency{
		{
			Name:        "curl",
			Command:     "curl",
			Optional:    false,
			InstallHint: "Usually pre-installed; if missing: apt-get install curl",
		},
		{
			Name:        "git",
			Command:     "git",
			Optional:    false,
			InstallHint: "Install with: apt-get install git",
		},
		{
			Name:        "make",
			Command:     "make",
			Optional:    false,
			InstallHint: "Install with: apt-get install make",
		},
	}

	var missing []Dependency
	for _, dep := range dependencies {
		if _, err := exec.LookPath(dep.Command); err != nil {
			if !dep.Optional || !dc.skipOptional {
				missing = append(missing, dep)
			}
		}
	}

	if len(missing) > 0 {
		errMsg := "missing required dependencies:\n"
		for _, dep := range missing {
			errMsg += fmt.Sprintf("  - %s (%s): %s\n", dep.Name, dep.Command, dep.InstallHint)
		}
		return missing, fmt.Errorf("%s", errMsg)
	}

	return nil, nil
}

// ExternalToolChecker validates external tool versions and availability
type ExternalToolChecker struct{}

// CheckIPFSAvailable checks if IPFS is available in PATH
func (etc *ExternalToolChecker) CheckIPFSAvailable() bool {
	_, err := exec.LookPath("ipfs")
	return err == nil
}

// CheckIPFSClusterAvailable checks if IPFS Cluster Service is available
func (etc *ExternalToolChecker) CheckIPFSClusterAvailable() bool {
	_, err := exec.LookPath("ipfs-cluster-service")
	return err == nil
}

// CheckRQLiteAvailable checks if RQLite is available
func (etc *ExternalToolChecker) CheckRQLiteAvailable() bool {
	_, err := exec.LookPath("rqlited")
	return err == nil
}

// CheckOlricAvailable checks if Olric Server is available
func (etc *ExternalToolChecker) CheckOlricAvailable() bool {
	_, err := exec.LookPath("olric-server")
	return err == nil
}

// CheckAnonAvailable checks if Anon is available (optional)
func (etc *ExternalToolChecker) CheckAnonAvailable() bool {
	_, err := exec.LookPath("anon")
	return err == nil
}

// CheckGoAvailable checks if Go is installed
func (etc *ExternalToolChecker) CheckGoAvailable() bool {
	_, err := exec.LookPath("go")
	return err == nil
}

// ResourceChecker validates system resources for production deployment
type ResourceChecker struct{}

// NewResourceChecker creates a new resource checker
func NewResourceChecker() *ResourceChecker {
	return &ResourceChecker{}
}

// CheckDiskSpace validates sufficient disk space (minimum 10GB free)
func (rc *ResourceChecker) CheckDiskSpace(path string) error {
	checkPath := path

	// If the path doesn't exist, check the parent directory instead
	for checkPath != "/" {
		if _, err := os.Stat(checkPath); err == nil {
			break
		}
		checkPath = filepath.Dir(checkPath)
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(checkPath, &stat); err != nil {
		return fmt.Errorf("failed to check disk space: %w", err)
	}

	// Available space in bytes
	availableBytes := stat.Bavail * uint64(stat.Bsize)
	minRequiredBytes := uint64(10 * 1024 * 1024 * 1024) // 10GB

	if availableBytes < minRequiredBytes {
		availableGB := float64(availableBytes) / (1024 * 1024 * 1024)
		return fmt.Errorf("insufficient disk space: %.1fGB available, minimum 10GB required", availableGB)
	}

	return nil
}

// CheckRAM validates sufficient RAM (minimum 2GB total)
func (rc *ResourceChecker) CheckRAM() error {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return fmt.Errorf("failed to read memory info: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	totalKB := uint64(0)

	for _, line := range lines {
		if strings.HasPrefix(line, "MemTotal:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if kb, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
					totalKB = kb
					break
				}
			}
		}
	}

	if totalKB == 0 {
		return fmt.Errorf("could not determine total RAM")
	}

	minRequiredKB := uint64(2 * 1024 * 1024) // 2GB in KB
	if totalKB < minRequiredKB {
		totalGB := float64(totalKB) / (1024 * 1024)
		return fmt.Errorf("insufficient RAM: %.1fGB total, minimum 2GB required", totalGB)
	}

	return nil
}

// CheckCPU validates sufficient CPU cores (minimum 2 cores)
func (rc *ResourceChecker) CheckCPU() error {
	cores := runtime.NumCPU()
	if cores < 2 {
		return fmt.Errorf("insufficient CPU cores: %d available, minimum 2 required", cores)
	}
	return nil
}
