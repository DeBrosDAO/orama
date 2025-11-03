package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"gopkg.in/yaml.v3"
)

// HandleRQLiteCommand handles rqlite-related commands
func HandleRQLiteCommand(args []string) {
	if len(args) == 0 {
		showRQLiteHelp()
		return
	}

	if runtime.GOOS != "linux" {
		fmt.Fprintf(os.Stderr, "❌ RQLite commands are only supported on Linux\n")
		os.Exit(1)
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "fix":
		handleRQLiteFix(subargs)
	case "help":
		showRQLiteHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown rqlite subcommand: %s\n", subcommand)
		showRQLiteHelp()
		os.Exit(1)
	}
}

func showRQLiteHelp() {
	fmt.Printf("🗄️  RQLite Commands\n\n")
	fmt.Printf("Usage: network-cli rqlite <subcommand> [options]\n\n")
	fmt.Printf("Subcommands:\n")
	fmt.Printf("  fix                    - Fix misconfigured join address and clean stale raft state\n\n")
	fmt.Printf("Description:\n")
	fmt.Printf("  The 'fix' command automatically repairs common rqlite cluster issues:\n")
	fmt.Printf("  - Corrects join address from HTTP port (5001) to Raft port (7001) if misconfigured\n")
	fmt.Printf("  - Cleans stale raft state that prevents proper cluster formation\n")
	fmt.Printf("  - Restarts the node service with corrected configuration\n\n")
	fmt.Printf("Requirements:\n")
	fmt.Printf("  - Must be run as root (use sudo)\n")
	fmt.Printf("  - Only works on non-bootstrap nodes (nodes with join_address configured)\n")
	fmt.Printf("  - Stops and restarts the debros-node service\n\n")
	fmt.Printf("Examples:\n")
	fmt.Printf("  sudo network-cli rqlite fix\n")
}

func handleRQLiteFix(args []string) {
	requireRoot()

	// Parse optional flags
	dryRun := false
	for _, arg := range args {
		if arg == "--dry-run" || arg == "-n" {
			dryRun = true
		}
	}

	if dryRun {
		fmt.Printf("🔍 Dry-run mode - no changes will be made\n\n")
	}

	fmt.Printf("🔧 RQLite Cluster Repair\n\n")

	// Load config
	configPath, err := config.DefaultPath("node.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to determine config path: %v\n", err)
		os.Exit(1)
	}

	cfg, err := loadConfigForRepair(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Check if this is a bootstrap node
	if cfg.Node.Type == "bootstrap" || cfg.Database.RQLiteJoinAddress == "" {
		fmt.Printf("ℹ️  This is a bootstrap node (no join address configured)\n")
		fmt.Printf("   Bootstrap nodes don't need repair - they are the cluster leader\n")
		fmt.Printf("   Run this command on follower nodes instead\n")
		return
	}

	joinAddr := cfg.Database.RQLiteJoinAddress

	// Check if join address needs fixing
	needsConfigFix := needsFix(joinAddr, cfg.Database.RQLiteRaftPort, cfg.Database.RQLitePort)
	var fixedAddr string

	if needsConfigFix {
		fmt.Printf("⚠️  Detected misconfigured join address: %s\n", joinAddr)
		fmt.Printf("   Expected Raft port (%d) but found HTTP port (%d)\n", cfg.Database.RQLiteRaftPort, cfg.Database.RQLitePort)

		// Extract host from join address
		host, _, err := parseJoinAddress(joinAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to parse join address: %v\n", err)
			os.Exit(1)
		}

		// Fix the join address - rqlite expects Raft port for -join
		fixedAddr = fmt.Sprintf("%s:%d", host, cfg.Database.RQLiteRaftPort)
		fmt.Printf("   Corrected address: %s\n\n", fixedAddr)
	} else {
		fmt.Printf("✅ Join address looks correct: %s\n", joinAddr)
		fmt.Printf("   Will clean stale raft state to ensure proper cluster formation\n\n")
		fixedAddr = joinAddr // No change needed
	}

	if dryRun {
		fmt.Printf("🔍 Dry-run: Would clean raft state")
		if needsConfigFix {
			fmt.Printf(" and fix config")
		}
		fmt.Printf("\n")
		return
	}

	// Stop the service
	fmt.Printf("⏹️  Stopping debros-node service...\n")
	if err := stopService("debros-node"); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to stop service: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   ✓ Service stopped\n\n")

	// Update config file if needed
	if needsConfigFix {
		fmt.Printf("📝 Updating configuration file...\n")
		if err := updateConfigJoinAddress(configPath, fixedAddr); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to update config: %v\n", err)
			fmt.Fprintf(os.Stderr, "   Service is stopped - please fix manually and restart\n")
			os.Exit(1)
		}
		fmt.Printf("   ✓ Config updated: %s\n\n", configPath)
	}

	// Clean raft state
	fmt.Printf("🧹 Cleaning stale raft state...\n")
	dataDir := expandDataDir(cfg.Node.DataDir)
	raftDir := filepath.Join(dataDir, "rqlite", "raft")
	if err := cleanRaftState(raftDir); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to clean raft state: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Continuing anyway - raft state may still exist\n")
	} else {
		fmt.Printf("   ✓ Raft state cleaned\n\n")
	}

	// Restart the service
	fmt.Printf("🚀 Restarting debros-node service...\n")
	if err := startService("debros-node"); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to start service: %v\n", err)
		fmt.Fprintf(os.Stderr, "   Config has been fixed - please restart manually:\n")
		fmt.Fprintf(os.Stderr, "   sudo systemctl start debros-node\n")
		os.Exit(1)
	}
	fmt.Printf("   ✓ Service started\n\n")

	fmt.Printf("✅ Repair complete!\n\n")
	fmt.Printf("The node should now join the cluster correctly.\n")
	fmt.Printf("Monitor logs with: sudo network-cli service logs node --follow\n")
}

func loadConfigForRepair(path string) (*config.Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var cfg config.Config
	if err := config.DecodeStrict(file, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &cfg, nil
}

func needsFix(joinAddr string, raftPort int, httpPort int) bool {
	if joinAddr == "" {
		return false
	}

	// Remove http:// or https:// prefix if present
	addr := joinAddr
	if strings.HasPrefix(addr, "http://") {
		addr = strings.TrimPrefix(addr, "http://")
	} else if strings.HasPrefix(addr, "https://") {
		addr = strings.TrimPrefix(addr, "https://")
	}

	// Parse host:port
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false // Can't parse, assume it's fine
	}

	// Check if port matches HTTP port (incorrect - should be Raft port)
	if port == fmt.Sprintf("%d", httpPort) {
		return true
	}

	// If it matches Raft port, it's correct
	if port == fmt.Sprintf("%d", raftPort) {
		return false
	}

	// Unknown port - assume it's fine
	return false
}

func parseJoinAddress(joinAddr string) (host, port string, err error) {
	// Remove http:// or https:// prefix if present
	addr := joinAddr
	if strings.HasPrefix(addr, "http://") {
		addr = strings.TrimPrefix(addr, "http://")
	} else if strings.HasPrefix(addr, "https://") {
		addr = strings.TrimPrefix(addr, "https://")
	}

	host, port, err = net.SplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("invalid join address format: %w", err)
	}

	return host, port, nil
}

func updateConfigJoinAddress(configPath string, newJoinAddr string) error {
	// Read the file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML into a generic map to preserve structure
	var yamlData map[string]interface{}
	if err := yaml.Unmarshal(data, &yamlData); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Navigate to database.rqlite_join_address
	database, ok := yamlData["database"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("database section not found in config")
	}

	database["rqlite_join_address"] = newJoinAddr

	// Write back to file
	updatedData, err := yaml.Marshal(yamlData)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	if err := os.WriteFile(configPath, updatedData, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

func expandDataDir(dataDir string) string {
	expanded := os.ExpandEnv(dataDir)
	if strings.HasPrefix(expanded, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return expanded // Fallback to original
		}
		expanded = filepath.Join(home, expanded[1:])
	}
	return expanded
}

func cleanRaftState(raftDir string) error {
	if _, err := os.Stat(raftDir); os.IsNotExist(err) {
		return nil // Directory doesn't exist, nothing to clean
	}

	// Remove raft state files
	filesToRemove := []string{
		"peers.json",
		"peers.json.backup",
		"peers.info",
		"raft.db",
	}

	for _, file := range filesToRemove {
		filePath := filepath.Join(raftDir, file)
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove %s: %w", filePath, err)
		}
	}

	return nil
}

func stopService(serviceName string) error {
	cmd := exec.Command("systemctl", "stop", serviceName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl stop failed: %w", err)
	}
	return nil
}

func startService(serviceName string) error {
	cmd := exec.Command("systemctl", "start", serviceName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl start failed: %w", err)
	}
	return nil
}
