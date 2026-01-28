package utils

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

var ErrServiceNotFound = errors.New("service not found")

// PortSpec defines a port and its name for checking availability
type PortSpec struct {
	Name string
	Port int
}

var ServicePorts = map[string][]PortSpec{
	"debros-gateway": {
		{Name: "Gateway API", Port: 6001},
	},
	"debros-olric": {
		{Name: "Olric HTTP", Port: 3320},
		{Name: "Olric Memberlist", Port: 3322},
	},
	"debros-node": {
		{Name: "RQLite HTTP", Port: 5001},
		{Name: "RQLite Raft", Port: 7001},
	},
	"debros-ipfs": {
		{Name: "IPFS API", Port: 4501},
		{Name: "IPFS Gateway", Port: 8080},
		{Name: "IPFS Swarm", Port: 4101},
	},
	"debros-ipfs-cluster": {
		{Name: "IPFS Cluster API", Port: 9094},
	},
}

// DefaultPorts is used for fresh installs/upgrades before unit files exist.
func DefaultPorts() []PortSpec {
	return []PortSpec{
		{Name: "IPFS Swarm", Port: 4001},
		{Name: "IPFS API", Port: 4501},
		{Name: "IPFS Gateway", Port: 8080},
		{Name: "Gateway API", Port: 6001},
		{Name: "RQLite HTTP", Port: 5001},
		{Name: "RQLite Raft", Port: 7001},
		{Name: "IPFS Cluster API", Port: 9094},
		{Name: "Olric HTTP", Port: 3320},
		{Name: "Olric Memberlist", Port: 3322},
	}
}

// ResolveServiceName resolves service aliases to actual systemd service names
func ResolveServiceName(alias string) ([]string, error) {
	// Service alias mapping (unified - no bootstrap/node distinction)
	aliases := map[string][]string{
		"node":         {"debros-node"},
		"ipfs":         {"debros-ipfs"},
		"cluster":      {"debros-ipfs-cluster"},
		"ipfs-cluster": {"debros-ipfs-cluster"},
		"gateway":      {"debros-gateway"},
		"olric":        {"debros-olric"},
		"rqlite":       {"debros-node"}, // RQLite logs are in node logs
	}

	// Check if it's an alias
	if serviceNames, ok := aliases[strings.ToLower(alias)]; ok {
		// Filter to only existing services
		var existing []string
		for _, svc := range serviceNames {
			unitPath := filepath.Join("/etc/systemd/system", svc+".service")
			if _, err := os.Stat(unitPath); err == nil {
				existing = append(existing, svc)
			}
		}
		if len(existing) == 0 {
			return nil, fmt.Errorf("no services found for alias %q", alias)
		}
		return existing, nil
	}

	// Check if it's already a full service name
	unitPath := filepath.Join("/etc/systemd/system", alias+".service")
	if _, err := os.Stat(unitPath); err == nil {
		return []string{alias}, nil
	}

	// Try without .service suffix
	if !strings.HasSuffix(alias, ".service") {
		unitPath = filepath.Join("/etc/systemd/system", alias+".service")
		if _, err := os.Stat(unitPath); err == nil {
			return []string{alias}, nil
		}
	}

	return nil, fmt.Errorf("service %q not found. Use: node, ipfs, cluster, gateway, olric, or full service name", alias)
}

// IsServiceActive checks if a systemd service is currently active (running)
func IsServiceActive(service string) (bool, error) {
	cmd := exec.Command("systemctl", "is-active", "--quiet", service)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 3:
				return false, nil
			case 4:
				return false, ErrServiceNotFound
			}
		}
		return false, err
	}
	return true, nil
}

// IsServiceEnabled checks if a systemd service is enabled to start on boot
func IsServiceEnabled(service string) (bool, error) {
	cmd := exec.Command("systemctl", "is-enabled", "--quiet", service)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 1:
				return false, nil // Service is disabled
			case 4:
				return false, ErrServiceNotFound
			}
		}
		return false, err
	}
	return true, nil
}

// IsServiceMasked checks if a systemd service is masked
func IsServiceMasked(service string) (bool, error) {
	cmd := exec.Command("systemctl", "is-enabled", service)
	output, err := cmd.CombinedOutput()
	if err != nil {
		outputStr := string(output)
		if strings.Contains(outputStr, "masked") {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// GetProductionServices returns a list of all DeBros production service names that exist
func GetProductionServices() []string {
	// Unified service names (no bootstrap/node distinction)
	allServices := []string{
		"debros-gateway",
		"debros-node",
		"debros-olric",
		"debros-ipfs-cluster",
		"debros-ipfs",
		"debros-anyone-client",
		"debros-anyone-relay",
	}

	// Filter to only existing services by checking if unit file exists
	var existing []string
	for _, svc := range allServices {
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		if _, err := os.Stat(unitPath); err == nil {
			existing = append(existing, svc)
		}
	}

	return existing
}

// CollectPortsForServices returns a list of ports used by the specified services
func CollectPortsForServices(services []string, skipActive bool) ([]PortSpec, error) {
	seen := make(map[int]PortSpec)
	for _, svc := range services {
		if skipActive {
			active, err := IsServiceActive(svc)
			if err != nil {
				return nil, fmt.Errorf("unable to check %s: %w", svc, err)
			}
			if active {
				continue
			}
		}
		for _, spec := range ServicePorts[svc] {
			if _, ok := seen[spec.Port]; !ok {
				seen[spec.Port] = spec
			}
		}
	}
	ports := make([]PortSpec, 0, len(seen))
	for _, spec := range seen {
		ports = append(ports, spec)
	}
	return ports, nil
}

// EnsurePortsAvailable checks if the specified ports are available
func EnsurePortsAvailable(action string, ports []PortSpec) error {
	for _, spec := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", spec.Port))
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use") {
				return fmt.Errorf("%s cannot continue: %s (port %d) is already in use", action, spec.Name, spec.Port)
			}
			return fmt.Errorf("%s cannot continue: failed to inspect %s (port %d): %w", action, spec.Name, spec.Port, err)
		}
		_ = ln.Close()
	}
	return nil
}

