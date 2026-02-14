package uninstall

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Handle executes the uninstall command
func Handle() {
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "❌ Production uninstall must be run as root (use sudo)\n")
		os.Exit(1)
	}

	fmt.Printf("⚠️  This will stop and remove all Orama production services\n")
	fmt.Printf("⚠️  Configuration and data will be preserved in /home/orama/.orama\n\n")
	fmt.Printf("Continue? (yes/no): ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))

	if response != "yes" && response != "y" {
		fmt.Printf("Uninstall cancelled\n")
		return
	}

	services := []string{
		"orama-gateway",
		"orama-node",
		"orama-olric",
		"orama-ipfs-cluster",
		"orama-ipfs",
		"orama-anyone-client",
	}

	fmt.Printf("Stopping services...\n")
	for _, svc := range services {
		exec.Command("systemctl", "stop", svc).Run()
		exec.Command("systemctl", "disable", svc).Run()
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		os.Remove(unitPath)
	}

	exec.Command("systemctl", "daemon-reload").Run()
	fmt.Printf("✅ Services uninstalled\n")
	fmt.Printf("   Configuration and data preserved in /home/orama/.orama\n")
	fmt.Printf("   To remove all data: rm -rf /home/orama/.orama\n\n")
}
