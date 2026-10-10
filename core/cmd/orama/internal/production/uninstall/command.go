package uninstall

import (
	"bufio"
	"fmt"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/install/installers"
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

	// Host-level leftovers from before the factory split. The live gateway is
	// orama-namespace-gateway@index (and @<tenant>), stopped above as a namespace unit.
	services := []string{
		"orama-gateway", // Legacy: kept for cleanup of old installs
		"orama-node",
		"orama-olric",
		"orama-ipfs-cluster",
		"orama-ipfs",
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

	// The Anyone network is no longer part of Orama at all; a node that was
	// never upgraded past it still has its units running.
	fmt.Printf("Removing the legacy Anyone network...\n")
	if err := installers.NewLegacyAnyoneCleaner(install.OramaDir, os.Stdout).Remove(); err != nil {
		return fmt.Errorf("remove the legacy Anyone network: %w", err)
	}

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
