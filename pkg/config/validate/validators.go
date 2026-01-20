package validate

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ValidationError represents a single validation error with context.
type ValidationError struct {
	Path    string // e.g., "discovery.bootstrap_peers[0]" or "discovery.peers[0]"
	Message string // e.g., "invalid multiaddr"
	Hint    string // e.g., "expected /ip{4,6}/.../tcp/<port>/p2p/<peerID>"
}

func (e ValidationError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("%s: %s; %s", e.Path, e.Message, e.Hint)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// ValidateDataDir validates that a data directory exists or can be created.
func ValidateDataDir(path string) error {
	if path == "" {
		return fmt.Errorf("must not be empty")
	}

	// Expand ~ to home directory
	expandedPath := os.ExpandEnv(path)
	if strings.HasPrefix(expandedPath, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot determine home directory: %v", err)
		}
		expandedPath = filepath.Join(home, expandedPath[1:])
	}

	if info, err := os.Stat(expandedPath); err == nil {
		// Directory exists; check if it's a directory and writable
		if !info.IsDir() {
			return fmt.Errorf("path exists but is not a directory")
		}
		// Try to write a test file to check permissions
		testFile := filepath.Join(expandedPath, ".write_test")
		if err := os.WriteFile(testFile, []byte(""), 0644); err != nil {
			return fmt.Errorf("directory not writable: %v", err)
		}
		os.Remove(testFile)
	} else if os.IsNotExist(err) {
		// Directory doesn't exist; check if parent is writable
		parent := filepath.Dir(expandedPath)
		if parent == "" || parent == "." {
			parent = "."
		}
		// Allow parent not existing - it will be created at runtime
		if info, err := os.Stat(parent); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("parent directory not accessible: %v", err)
			}
			// Parent doesn't exist either - that's ok, will be created
		} else if !info.IsDir() {
			return fmt.Errorf("parent path is not a directory")
		} else {
			// Parent exists, check if writable
			if err := ValidateDirWritable(parent); err != nil {
				return fmt.Errorf("parent directory not writable: %v", err)
			}
		}
	} else {
		return fmt.Errorf("cannot access path: %v", err)
	}

	return nil
}

// ValidateDirWritable validates that a directory exists and is writable.
func ValidateDirWritable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot access directory: %v", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}

	// Try to write a test file
	testFile := filepath.Join(path, ".write_test")
	if err := os.WriteFile(testFile, []byte(""), 0644); err != nil {
		return fmt.Errorf("directory not writable: %v", err)
	}
	os.Remove(testFile)

	return nil
}

// ValidateFileReadable validates that a file exists and is readable.
func ValidateFileReadable(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot read file: %v", err)
	}
	return nil
}

// ValidateHostPort validates a host:port address format.
func ValidateHostPort(hostPort string) error {
	parts := strings.Split(hostPort, ":")
	if len(parts) != 2 {
		return fmt.Errorf("expected format host:port")
	}

	host := parts[0]
	port := parts[1]

	if host == "" {
		return fmt.Errorf("host must not be empty")
	}

	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		return fmt.Errorf("port must be a number between 1 and 65535; got %q", port)
	}

	return nil
}

// ValidateHostOrHostPort validates either a hostname or host:port format.
func ValidateHostOrHostPort(addr string) error {
	// Try to parse as host:port first
	if strings.Contains(addr, ":") {
		return ValidateHostPort(addr)
	}

	// Otherwise just check if it's a valid hostname/IP
	if addr == "" {
		return fmt.Errorf("address must not be empty")
	}

	return nil
}

// ValidatePort validates that a port number is in the valid range.
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535; got %d", port)
	}
	return nil
}

// ExtractTCPPort extracts the TCP port from a multiaddr string.
func ExtractTCPPort(multiaddrStr string) string {
	// Look for the /tcp/ protocol code
	parts := strings.Split(multiaddrStr, "/")
	for i := 0; i < len(parts); i++ {
		if parts[i] == "tcp" {
			// The port is the next part
			if i+1 < len(parts) {
				return parts[i+1]
			}
			break
		}
	}
	return ""
}

// ValidateSwarmKey validates that a swarm key is 64 hex characters.
func ValidateSwarmKey(key string) error {
	key = strings.TrimSpace(key)
	if len(key) != 64 {
		return fmt.Errorf("swarm key must be 64 hex characters (32 bytes), got %d", len(key))
	}
	if _, err := hex.DecodeString(key); err != nil {
		return fmt.Errorf("swarm key must be valid hexadecimal: %w", err)
	}
	return nil
}
