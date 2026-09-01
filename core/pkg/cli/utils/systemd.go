package utils

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"gopkg.in/yaml.v3"
)

var ErrServiceNotFound = errors.New("service not found")

// PortSpec defines a port and its name for checking availability
type PortSpec struct {
	Name string
	Port int
}

var ServicePorts = map[string][]PortSpec{
	"orama-olric": {
		{Name: "Olric HTTP", Port: constants.OlricHTTPPort},
		{Name: "Olric Memberlist", Port: constants.OlricMemberlistPort},
	},
	"orama-node": {
		{Name: "Gateway API", Port: constants.GatewayAPIPort}, // Gateway is embedded in orama-node
		{Name: "RQLite HTTP", Port: constants.RQLiteHTTPPort},
		{Name: "RQLite Raft", Port: constants.RQLiteRaftPort},
	},
	"orama-ipfs": {
		{Name: "IPFS API", Port: 4501},
		{Name: "IPFS Gateway", Port: 8080},
		{Name: "IPFS Swarm", Port: 4101},
	},
	"orama-ipfs-cluster": {
		{Name: "IPFS Cluster API", Port: 9094},
	},
}

// DefaultPorts is used for fresh installs/upgrades before unit files exist.
func DefaultPorts() []PortSpec {
	return []PortSpec{
		{Name: "IPFS Swarm", Port: 4001},
		{Name: "IPFS API", Port: 4501},
		{Name: "IPFS Gateway", Port: 8080},
		{Name: "Gateway API", Port: constants.GatewayAPIPort},
		{Name: "RQLite HTTP", Port: constants.RQLiteHTTPPort},
		{Name: "RQLite Raft", Port: constants.RQLiteRaftPort},
		{Name: "IPFS Cluster API", Port: 9094},
		{Name: "Olric HTTP", Port: constants.OlricHTTPPort},
		{Name: "Olric Memberlist", Port: constants.OlricMemberlistPort},
	}
}

// ResolveServiceName resolves service aliases to actual systemd service names
func ResolveServiceName(alias string) ([]string, error) {
	// Service alias mapping (unified - no bootstrap/node distinction)
	aliases := map[string][]string{
		"node":         {"orama-node"},
		"ipfs":         {"orama-ipfs"},
		"cluster":      {"orama-ipfs-cluster"},
		"ipfs-cluster": {"orama-ipfs-cluster"},
		"gateway":      {"orama-node"}, // Gateway is embedded in orama-node
		"olric":        {"orama-olric"},
		"rqlite":       {"orama-node"}, // RQLite logs are in node logs
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

// ServiceUnitExists reports whether a systemd unit file is installed for the
// given service name (e.g. "caddy"). Used to guard restart/start logic so it
// only touches services actually present on this node.
func ServiceUnitExists(service string) bool {
	_, err := os.Stat(filepath.Join("/etc/systemd/system", service+".service"))
	return err == nil
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

// GetProductionServices returns a list of all Orama production service names that exist,
// including both global services and namespace-specific services
func GetProductionServices() []string {
	// Global/default service names
	globalServices := []string{
		"orama-node",
		"orama-olric",
		"orama-ipfs-cluster",
		"orama-ipfs",
		"orama-vault",
		"orama-anyone-client",
		"orama-anyone-relay",
	}

	var existing []string

	// Add existing global services
	for _, svc := range globalServices {
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		if _, err := os.Stat(unitPath); err == nil {
			existing = append(existing, svc)
		}
	}

	// Discover namespace service instances from the namespaces data directory.
	// We can't rely on scanning /etc/systemd/system because that only contains
	// template files (e.g. orama-namespace-gateway@.service) with no instance name.
	// Restarting a template without an instance is a no-op.
	// Instead, scan the data directory where each subdirectory is a provisioned namespace.
	namespacesDir := "/opt/orama/.orama/data/namespaces"
	nsEntries, err := os.ReadDir(namespacesDir)
	if err == nil {
		serviceTypes := []string{"rqlite", "olric", "gateway", "sfu", "turn", "pubsub"}
		for _, nsEntry := range nsEntries {
			if !nsEntry.IsDir() {
				continue
			}
			ns := nsEntry.Name()
			for _, svcType := range serviceTypes {
				// Only add if the env file exists (service was provisioned)
				envFile := filepath.Join(namespacesDir, ns, svcType+".env")
				if _, err := os.Stat(envFile); err == nil {
					svcName := fmt.Sprintf("orama-namespace-%s@%s", svcType, ns)
					existing = append(existing, svcName)
				}
			}
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

// EnsurePortsAvailable checks if the specified ports are available.
// If a port is in use, it identifies the process and gives actionable guidance.
func EnsurePortsAvailable(action string, ports []PortSpec) error {
	var conflicts []string
	for _, spec := range ports {
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", spec.Port))
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use") {
				processInfo := identifyPortProcess(spec.Port)
				conflicts = append(conflicts, fmt.Sprintf("  - %s (port %d): %s", spec.Name, spec.Port, processInfo))
				continue
			}
			return fmt.Errorf("%s cannot continue: failed to inspect %s (port %d): %w", action, spec.Name, spec.Port, err)
		}
		_ = ln.Close()
	}
	if len(conflicts) > 0 {
		msg := fmt.Sprintf("%s cannot continue: the following ports are already in use:\n%s\n\n", action, strings.Join(conflicts, "\n"))
		msg += "Please stop the conflicting services before running this command.\n"
		msg += "Common fixes:\n"
		msg += "  - Docker:           sudo systemctl stop docker docker.socket\n"
		msg += "  - Old IPFS:         sudo systemctl stop ipfs\n"
		msg += "  - systemd-resolved: already handled by installer (port 53)\n"
		msg += "  - Other services:   sudo kill <PID> or sudo systemctl stop <service>"
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// identifyPortProcess uses ss/lsof to find what process is using a port
func identifyPortProcess(port int) string {
	// Try ss first (available on most Linux)
	out, err := exec.Command("ss", "-tlnp", fmt.Sprintf("sport = :%d", port)).CombinedOutput()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			if strings.Contains(line, "users:") {
				// Extract process info from ss output like: users:(("docker-proxy",pid=2049,fd=4))
				if idx := strings.Index(line, "users:"); idx != -1 {
					return strings.TrimSpace(line[idx:])
				}
			}
		}
	}

	// Fallback: try lsof
	out, err = exec.Command("lsof", "-i", fmt.Sprintf(":%d", port), "-sTCP:LISTEN", "-n", "-P").CombinedOutput()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 1 {
			return strings.TrimSpace(lines[1]) // first data line after header
		}
	}

	return "unknown process"
}

// NamespaceServiceOrder defines the dependency order for namespace services.
// RQLite must start first (database), then Olric (cache), then Gateway (depends on both).
// TURN and SFU are optional WebRTC services that start after Gateway.
var NamespaceServiceOrder = []string{"rqlite", "olric", "gateway", "turn", "sfu"}

// StartServicesOrdered starts services respecting namespace dependency order.
// Namespace services are started in order: rqlite → olric (+ wait) → gateway.
// Non-namespace services are started after.
// The action parameter is the systemctl command (e.g., "start" or "restart").
func StartServicesOrdered(services []string, action string) {
	// Separate namespace services by type, and collect non-namespace services
	nsServices := make(map[string][]string) // svcType → []svcName
	var other []string

	for _, svc := range services {
		matched := false
		for _, svcType := range NamespaceServiceOrder {
			prefix := "orama-namespace-" + svcType + "@"
			if strings.HasPrefix(svc, prefix) {
				nsServices[svcType] = append(nsServices[svcType], svc)
				matched = true
				break
			}
		}
		if !matched {
			other = append(other, svc)
		}
	}

	// Start namespace services in dependency order
	for _, svcType := range NamespaceServiceOrder {
		svcs := nsServices[svcType]
		for _, svc := range svcs {
			fmt.Printf("  %s%sing %s...\n", strings.ToUpper(action[:1]), action[1:], svc)
			if err := exec.Command("systemctl", action, svc).Run(); err != nil {
				fmt.Printf("  ⚠️  Failed to %s %s: %v\n", action, svc, err)
			} else {
				fmt.Printf("  ✓ %s\n", svc)
			}
		}

		// After starting all Olric instances, wait for each one's memberlist
		// port to accept TCP connections before starting gateways. Without this,
		// gateways start before Olric is ready and the Olric client initialization
		// fails permanently (no retry).
		if svcType == "olric" && len(svcs) > 0 {
			fmt.Printf("  Waiting for namespace Olric instances to become ready...\n")
			for _, svc := range svcs {
				ns := strings.TrimPrefix(svc, "orama-namespace-olric@")
				port := getOlricMemberlistPort(ns)
				if port <= 0 {
					fmt.Printf("  ⚠️  Could not determine Olric memberlist port for namespace %s\n", ns)
					continue
				}
				if err := waitForTCPPort(port, 30*time.Second); err != nil {
					fmt.Printf("  ⚠️  Olric memberlist port %d not ready for namespace %s: %v\n", port, ns, err)
				} else {
					fmt.Printf("  ✓ Olric ready for namespace %s (port %d)\n", ns, port)
				}
			}
		}
	}

	// Start any remaining non-namespace services
	for _, svc := range other {
		fmt.Printf("  %s%sing %s...\n", strings.ToUpper(action[:1]), action[1:], svc)
		if err := exec.Command("systemctl", action, svc).Run(); err != nil {
			fmt.Printf("  ⚠️  Failed to %s %s: %v\n", action, svc, err)
		} else {
			fmt.Printf("  ✓ %s\n", svc)
		}
	}
}

// getOlricMemberlistPort reads a namespace's Olric config and returns the
// memberlist bind port. Returns 0 if the config cannot be read or parsed.
func getOlricMemberlistPort(namespace string) int {
	envFile := filepath.Join("/opt/orama/.orama/data/namespaces", namespace, "olric.env")
	f, err := os.Open(envFile)
	if err != nil {
		return 0
	}
	defer f.Close()

	// Read OLRIC_SERVER_CONFIG path from env file
	var configPath string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "OLRIC_SERVER_CONFIG=") {
			configPath = strings.TrimPrefix(line, "OLRIC_SERVER_CONFIG=")
			break
		}
	}
	if configPath == "" {
		return 0
	}

	// Parse the YAML config to extract memberlist.bindPort
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return 0
	}

	var cfg struct {
		Memberlist struct {
			BindPort int `yaml:"bindPort"`
		} `yaml:"memberlist"`
	}
	if err := yaml.Unmarshal(configData, &cfg); err != nil {
		return 0
	}

	return cfg.Memberlist.BindPort
}

// waitForTCPPort polls a TCP port until it accepts connections or the timeout expires.
func waitForTCPPort(port int, timeout time.Duration) error {
	addr := fmt.Sprintf("localhost:%d", port)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("port %d did not become ready within %s", port, timeout)
}
