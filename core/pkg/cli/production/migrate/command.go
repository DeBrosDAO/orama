package migrate

import (
	"fmt"
	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
	"os"
	"os/exec"
	"path/filepath"
)

// Options holds the flags for the migrate command.
type Options struct {
	DryRun bool
}

// Run executes the migrate command.
func Run(opts Options) error {
	dryRun := &opts.DryRun

	if !*dryRun {
		if err := clierr.RequireRoot("migrating the installation"); err != nil {
			return err
		}
	}

	oramaDir := "/opt/orama/.orama"

	fmt.Printf("🔄 Checking for installations to migrate...\n\n")

	// Check for old-style installations
	validator := NewValidator(oramaDir)
	needsMigration := validator.CheckNeedsMigration()

	if !needsMigration {
		fmt.Printf("\nNo migration needed: the installation already uses the unified structure\n")
		return nil
	}

	if *dryRun {
		fmt.Printf("\nDry run: no changes made\n")
		fmt.Printf("   Run without --dry-run to perform the migration\n")
		return nil
	}

	fmt.Printf("\n🔄 Starting migration...\n")

	// Stop old services first
	stopOldServices()

	// Migrate data directories
	migrateDataDirectories(oramaDir)

	// Migrate config files
	migrateConfigFiles(oramaDir)

	// Remove old services
	removeOldServices()

	// Reload systemd
	exec.Command("systemctl", "daemon-reload").Run()

	fmt.Printf("\nMigration complete.\n")
	fmt.Printf("   Run 'sudo orama node upgrade --restart' to regenerate services with new names\n\n")
	return nil
}

func stopOldServices() {
	oldServices := []string{
		"orama-ipfs",
		"orama-ipfs-cluster",
		"orama-node",
	}

	fmt.Printf("\n  Stopping old services...\n")
	for _, svc := range oldServices {
		if err := exec.Command("systemctl", "stop", svc).Run(); err == nil {
			fmt.Printf("    ✓ Stopped %s\n", svc)
		}
	}
}

func migrateDataDirectories(oramaDir string) {
	oldDataDirs := []string{
		filepath.Join(oramaDir, "data", "node-1"),
		filepath.Join(oramaDir, "data", "node"),
	}
	newDataDir := filepath.Join(oramaDir, "data")

	fmt.Printf("\n  Migrating data directories...\n")

	// Prefer node-1 data if it exists, otherwise use node data
	sourceDir := ""
	if _, err := os.Stat(filepath.Join(oramaDir, "data", "node-1")); err == nil {
		sourceDir = filepath.Join(oramaDir, "data", "node-1")
	} else if _, err := os.Stat(filepath.Join(oramaDir, "data", "node")); err == nil {
		sourceDir = filepath.Join(oramaDir, "data", "node")
	}

	if sourceDir != "" {
		// Move contents to unified data directory
		entries, _ := os.ReadDir(sourceDir)
		for _, entry := range entries {
			src := filepath.Join(sourceDir, entry.Name())
			dst := filepath.Join(newDataDir, entry.Name())
			if _, err := os.Stat(dst); os.IsNotExist(err) {
				if err := os.Rename(src, dst); err == nil {
					fmt.Printf("    ✓ Moved %s → %s\n", src, dst)
				}
			}
		}
	}

	// Remove old data directories
	for _, dir := range oldDataDirs {
		if err := os.RemoveAll(dir); err == nil {
			fmt.Printf("    ✓ Removed %s\n", dir)
		}
	}
}

func migrateConfigFiles(oramaDir string) {
	fmt.Printf("\n  Migrating config files...\n")
	oldNodeConfig := filepath.Join(oramaDir, "configs", "bootstrap.yaml")
	newNodeConfig := filepath.Join(oramaDir, "configs", "node.yaml")

	if _, err := os.Stat(oldNodeConfig); err == nil {
		if _, err := os.Stat(newNodeConfig); os.IsNotExist(err) {
			if err := os.Rename(oldNodeConfig, newNodeConfig); err == nil {
				fmt.Printf("    ✓ Renamed bootstrap.yaml → node.yaml\n")
			}
		} else {
			os.Remove(oldNodeConfig)
			fmt.Printf("    ✓ Removed old bootstrap.yaml (node.yaml already exists)\n")
		}
	}
}

func removeOldServices() {
	oldServices := []string{
		"orama-ipfs",
		"orama-ipfs-cluster",
		"orama-node",
	}

	fmt.Printf("\n  Removing old service files...\n")
	for _, svc := range oldServices {
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		if err := os.Remove(unitPath); err == nil {
			fmt.Printf("    ✓ Removed %s\n", unitPath)
		}
	}
}
