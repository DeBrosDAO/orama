package status

import (
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/pkg/cli/utils"
)

// Handle executes the status command
func Handle() {
	fmt.Printf("Production Environment Status\n\n")

	// Unified service names (no bootstrap/node distinction)
	serviceNames := []string{
		"debros-ipfs",
		"debros-ipfs-cluster",
		// Note: RQLite is managed by node process, not as separate service
		"debros-olric",
		"debros-node",
		"debros-gateway",
	}

	// Friendly descriptions
	descriptions := map[string]string{
		"debros-ipfs":         "IPFS Daemon",
		"debros-ipfs-cluster": "IPFS Cluster",
		"debros-olric":        "Olric Cache Server",
		"debros-node":         "DeBros Node (includes RQLite)",
		"debros-gateway":      "DeBros Gateway",
	}

	fmt.Printf("Services:\n")
	found := false
	for _, svc := range serviceNames {
		active, _ := utils.IsServiceActive(svc)
		status := "❌ Inactive"
		if active {
			status = "✅ Active"
			found = true
		}
		fmt.Printf("  %s: %s\n", status, descriptions[svc])
	}

	if !found {
		fmt.Printf("  (No services found - installation may be incomplete)\n")
	}

	fmt.Printf("\nDirectories:\n")
	oramaDir := "/home/debros/.orama"
	if _, err := os.Stat(oramaDir); err == nil {
		fmt.Printf("  ✅ %s exists\n", oramaDir)
	} else {
		fmt.Printf("  ❌ %s not found\n", oramaDir)
	}

	fmt.Printf("\nView logs with: orama prod logs <service>\n")
}
