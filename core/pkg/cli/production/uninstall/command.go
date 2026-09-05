package uninstall

import (
	"bufio"
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/systemd"
)

// Handle executes the uninstall command
func Handle() error {
	if err := clierr.RequireRoot("uninstalling the node services"); err != nil {
		return err
	}

	fmt.Printf("⚠️  This will stop and remove all Orama production services\n")
	fmt.Printf("⚠️  Configuration and data will be preserved in /opt/orama/.orama\n\n")
	fmt.Printf("Continue? (yes/no): ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	if response != "yes" && response != "y" {
		return clierr.Aborted("uninstall cancelled")
	}

	// Stop and remove namespace services first
	fmt.Printf("Stopping namespace services...\n")
	stopNamespaceServices()

	// All global services (orama-gateway is legacy — now embedded in orama-node)
	services := []string{
		"orama-gateway", // Legacy: kept for cleanup of old installs
		"orama-node",
		"orama-olric",
		"orama-ipfs-cluster",
		"orama-ipfs",
		"orama-anyone-client",
		"orama-anyone-relay",
		"coredns",
		"caddy",
	}

	fmt.Printf("Stopping global services...\n")
	for _, svc := range services {
		exec.Command("systemctl", "stop", svc).Run()
		exec.Command("systemctl", "disable", svc).Run()
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		os.Remove(unitPath)
	}

	// Remove namespace template unit files
	removeNamespaceTemplates()

	exec.Command("systemctl", "daemon-reload").Run()
	fmt.Printf("✅ Services uninstalled\n")
	fmt.Printf("   Configuration and data preserved in /opt/orama/.orama\n")
	fmt.Printf("   To remove all data: rm -rf /opt/orama/.orama\n\n")
	return nil
}

// stopNamespaceServices discovers and stops all running namespace services
func stopNamespaceServices() {
	cmd := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-pager", "--no-legend", "orama-namespace-*@*.service")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "orama-namespace-") {
			svc := fields[0]
			exec.Command("systemctl", "stop", svc).Run()
			exec.Command("systemctl", "disable", svc).Run()
			fmt.Printf("  Stopped %s\n", svc)
		}
	}
}

// removeNamespaceTemplates removes namespace template unit files
func removeNamespaceTemplates() {
	templatePatterns := systemd.TemplateUnits
	for _, pattern := range templatePatterns {
		unitPath := filepath.Join("/etc/systemd/system", pattern)
		if _, err := os.Stat(unitPath); err == nil {
			os.Remove(unitPath)
		}
	}
}
