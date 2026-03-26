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
		"orama-ipfs",
		"orama-ipfs-cluster",
		// Note: RQLite is managed by node process, not as separate service
		"orama-olric",
		"orama-node",
		// Note: gateway is embedded in orama-node, no separate service
	}

	// Friendly descriptions
	descriptions := map[string]string{
		"orama-ipfs":         "IPFS Daemon",
		"orama-ipfs-cluster": "IPFS Cluster",
		"orama-olric":        "Olric Cache Server",
		"orama-node":         "Orama Node (includes RQLite + Gateway)",
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
	oramaDir := "/opt/orama/.orama"
	if _, err := os.Stat(oramaDir); err == nil {
		fmt.Printf("  ✅ %s exists\n", oramaDir)
	} else {
		fmt.Printf("  ❌ %s not found\n", oramaDir)
	}

	fmt.Printf("\nView logs with: orama node logs <service>\n")
}
