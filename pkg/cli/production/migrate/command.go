package migrate

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Handle executes the migrate command
func Handle(args []string) {
	// Parse flags
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "Show what would be migrated without making changes")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "❌ Failed to parse flags: %v\n", err)
		os.Exit(1)
	}

	if os.Geteuid() != 0 && !*dryRun {
		fmt.Fprintf(os.Stderr, "❌ Migration must be run as root (use sudo)\n")
		os.Exit(1)
	}

	oramaDir := "/home/debros/.orama"

	fmt.Printf("🔄 Checking for installations to migrate...\n\n")

	// Check for old-style installations
	validator := NewValidator(oramaDir)
	needsMigration := validator.CheckNeedsMigration()

	if !needsMigration {
		fmt.Printf("\n✅ No migration needed - installation already uses unified structure\n")
		return
	}

	if *dryRun {
		fmt.Printf("\n📋 Dry run - no changes made\n")
		fmt.Printf("   Run without --dry-run to perform migration\n")
		return
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

	fmt.Printf("\n✅ Migration complete!\n")
	fmt.Printf("   Run 'sudo orama upgrade --restart' to regenerate services with new names\n\n")
}

func stopOldServices() {
	oldServices := []string{
		"debros-ipfs",
		"debros-ipfs-cluster",
		"debros-node",
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
		"debros-ipfs",
		"debros-ipfs-cluster",
		"debros-node",
	}

	fmt.Printf("\n  Removing old service files...\n")
	for _, svc := range oldServices {
		unitPath := filepath.Join("/etc/systemd/system", svc+".service")
		if err := os.Remove(unitPath); err == nil {
			fmt.Printf("    ✓ Removed %s\n", unitPath)
		}
	}
}
