package sandbox

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Reset tears down all sandbox infrastructure (floating IPs, firewall, SSH key)
// and removes the config file so the user can rerun setup from scratch.
// This is useful when switching datacenter locations (floating IPs are location-bound).
func Reset() error {
	fmt.Println("Sandbox Reset")
	fmt.Println("=============")
	fmt.Println()

	cfg, err := LoadConfig()
	if err != nil {
		// Config doesn't exist — just clean up any local files
		fmt.Println("No sandbox config found. Cleaning up local files...")
		return resetLocalFiles()
	}

	// Check for active sandboxes — refuse to reset if clusters are still running
	active, _ := FindActiveSandbox()
	if active != nil {
		return fmt.Errorf("active sandbox %q exists — run 'orama sandbox destroy' first", active.Name)
	}

	// Show what will be deleted
	fmt.Println("This will delete the following Hetzner resources:")
	for i, fip := range cfg.FloatingIPs {
		fmt.Printf("  Floating IP %d: %s (ID: %d)\n", i+1, fip.IP, fip.ID)
	}
	if cfg.FirewallID != 0 {
		fmt.Printf("  Firewall ID: %d\n", cfg.FirewallID)
	}
	if cfg.SSHKey.HetznerID != 0 {
		fmt.Printf("  SSH Key ID: %d\n", cfg.SSHKey.HetznerID)
	}
	fmt.Println()
	fmt.Println("Local files to remove:")
	fmt.Println("  ~/.orama/sandbox.yaml")
	fmt.Println("  ~/.orama/sandbox_key")
	fmt.Println("  ~/.orama/sandbox_key.pub")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Delete all sandbox resources? [y/N]: ")
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(strings.ToLower(choice))
	if choice != "y" && choice != "yes" {
		fmt.Println("Aborted.")
		return nil
	}

	client := NewHetznerClient(cfg.HetznerAPIToken)

	// Step 1: Delete floating IPs
	fmt.Println()
	fmt.Println("Deleting floating IPs...")
	for _, fip := range cfg.FloatingIPs {
		if err := client.DeleteFloatingIP(fip.ID); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not delete floating IP %s (ID %d): %v\n", fip.IP, fip.ID, err)
		} else {
			fmt.Printf("  Deleted %s (ID %d)\n", fip.IP, fip.ID)
		}
	}

	// Step 2: Delete firewall
	if cfg.FirewallID != 0 {
		fmt.Println("Deleting firewall...")
		if err := client.DeleteFirewall(cfg.FirewallID); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not delete firewall (ID %d): %v\n", cfg.FirewallID, err)
		} else {
			fmt.Printf("  Deleted firewall (ID %d)\n", cfg.FirewallID)
		}
	}

	// Step 3: Delete SSH key from Hetzner
	if cfg.SSHKey.HetznerID != 0 {
		fmt.Println("Deleting SSH key from Hetzner...")
		if err := client.DeleteSSHKey(cfg.SSHKey.HetznerID); err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: could not delete SSH key (ID %d): %v\n", cfg.SSHKey.HetznerID, err)
		} else {
			fmt.Printf("  Deleted SSH key (ID %d)\n", cfg.SSHKey.HetznerID)
		}
	}

	// Step 4: Remove local files
	if err := resetLocalFiles(); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Reset complete. All sandbox resources deleted.")
	fmt.Println()
	fmt.Println("Next: orama sandbox setup")
	return nil
}

// resetLocalFiles removes the sandbox config and SSH key files.
func resetLocalFiles() error {
	dir, err := configDir()
	if err != nil {
		return err
	}

	files := []string{
		dir + "/sandbox.yaml",
		dir + "/sandbox_key",
		dir + "/sandbox_key.pub",
	}

	fmt.Println("Removing local files...")
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			fmt.Fprintf(os.Stderr, "  Warning: could not remove %s: %v\n", f, err)
		} else {
			fmt.Printf("  Removed %s\n", f)
		}
	}

	return nil
}
