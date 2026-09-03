package lifecycle

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

const (
	maintenanceFlagPath = "/opt/orama/.orama/maintenance.flag"
)

// HandlePreUpgrade prepares the node for a safe rolling upgrade:
//  1. Checks quorum safety
//  2. Writes maintenance flag
//  3. Transfers leadership on the index RQLite if leader
//  4. Transfers leadership on each namespace RQLite
//  5. Waits 15s for metadata propagation (H5 fix)
func HandlePreUpgrade() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "Error: pre-upgrade must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("Pre-upgrade: preparing node for safe restart...\n")

	// 1. Check quorum safety
	if warning := checkQuorumSafety(); warning != "" {
		fmt.Fprintf(os.Stderr, "  UNSAFE: %s\n", warning)
		fmt.Fprintf(os.Stderr, "  Aborting pre-upgrade. Use 'orama stop --force' to override.\n")
		os.Exit(1)
	}
	fmt.Printf("  Quorum check passed\n")

	// 2. Write maintenance flag
	if err := os.MkdirAll(filepath.Dir(maintenanceFlagPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: failed to create flag directory: %v\n", err)
	}
	if err := os.WriteFile(maintenanceFlagPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: failed to write maintenance flag: %v\n", err)
	} else {
		fmt.Printf("  Maintenance flag written\n")
	}

	// 3. Transfer leadership on the index RQLite
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	fmt.Printf("  Checking index RQLite leadership (port %d)...\n", constants.RQLiteHTTPPort)
	if err := rqlite.TransferLeadership(constants.RQLiteHTTPPort, logger); err != nil {
		fmt.Printf("  Warning: global leadership transfer: %v\n", err)
	} else {
		fmt.Printf("  Global RQLite leadership handled\n")
	}

	// 4. Transfer leadership on each namespace RQLite
	nsPorts := getNamespaceRQLitePorts()
	for ns, port := range nsPorts {
		fmt.Printf("  Checking namespace '%s' RQLite leadership (port %d)...\n", ns, port)
		if err := rqlite.TransferLeadership(port, logger); err != nil {
			fmt.Printf("  Warning: namespace '%s' leadership transfer: %v\n", ns, err)
		} else {
			fmt.Printf("  Namespace '%s' RQLite leadership handled\n", ns)
		}
	}

	// 5. Wait for metadata propagation (H5 fix: 15s, not 3s)
	// The peer exchange cycle is 30s, but we force-triggered metadata updates
	// via leadership transfer. 15s is sufficient for at least one exchange cycle.
	fmt.Printf("  Waiting 15s for metadata propagation...\n")
	time.Sleep(15 * time.Second)

	fmt.Printf("Pre-upgrade complete. Node is ready for restart.\n")
}

// getNamespaceRQLitePorts scans namespace env files to find RQLite HTTP ports.
// Returns map of namespace_name → HTTP port.
func getNamespaceRQLitePorts() map[string]int {
	namespacesDir := "/opt/orama/.orama/data/namespaces"
	ports := make(map[string]int)

	entries, err := os.ReadDir(namespacesDir)
	if err != nil {
		return ports
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ns := entry.Name()
		envFile := filepath.Join(namespacesDir, ns, "rqlite.env")
		port := parseHTTPPortFromEnv(envFile)
		if port > 0 {
			ports[ns] = port
		}
	}

	return ports
}

// parseHTTPPortFromEnv reads an env file and extracts the HTTP port from
// the HTTP_ADDR=0.0.0.0:PORT line.
func parseHTTPPortFromEnv(envFile string) int {
	f, err := os.Open(envFile)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "HTTP_ADDR=") {
			addr := strings.TrimPrefix(line, "HTTP_ADDR=")
			// Format: 0.0.0.0:PORT
			if idx := strings.LastIndex(addr, ":"); idx >= 0 {
				if port, err := strconv.Atoi(addr[idx+1:]); err == nil {
					return port
				}
			}
		}
	}
	return 0
}
