package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/node"
	"go.uber.org/zap"
)

// setup_logger initializes a logger for the given component.
func setup_logger(component logging.Component) (logger *logging.ColoredLogger) {
	var err error

	logger, err = logging.NewColoredLogger(component, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}

	return logger
}

// parse_flags parses command-line flags and returns them.
func parse_flags() (configName *string, help *bool) {
	configName = flag.String("config", "node.yaml", "Config filename in ~/.orama (default: node.yaml)")
	help = flag.Bool("help", false, "Show help")
	flag.Parse()

	return
}

// LoadConfigFromYAML loads a config from a YAML file using strict decoding.
func LoadConfigFromYAML(path string) (*config.Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var cfg config.Config
	if err := config.DecodeStrict(file, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// check_if_should_open_help checks if the help flag is set and opens the help if it is
func check_if_should_open_help(help *bool) {
	if *help {
		flag.Usage()
		os.Exit(0)
	}
}

// select_data_dir validates that we can load the config from ~/.orama
func select_data_dir_check(configName *string) {
	logger := setup_logger(logging.ComponentNode)

	var configPath string
	var err error

	// Check if configName is an absolute path
	if filepath.IsAbs(*configName) {
		// Use absolute path directly
		configPath = *configName
	} else {
		// Ensure config directory exists and is writable
		_, err = config.EnsureConfigDir()
		if err != nil {
			logger.Error("Failed to ensure config directory", zap.Error(err))
			fmt.Fprintf(os.Stderr, "\n❌ Configuration Error:\n")
			fmt.Fprintf(os.Stderr, "Failed to create/access config directory: %v\n", err)
			fmt.Fprintf(os.Stderr, "\nPlease ensure:\n")
			fmt.Fprintf(os.Stderr, "  1. Home directory is accessible: %s\n", os.ExpandEnv("~"))
			fmt.Fprintf(os.Stderr, "  2. You have write permissions to home directory\n")
			fmt.Fprintf(os.Stderr, "  3. Disk space is available\n")
			os.Exit(1)
		}

		configPath, err = config.DefaultPath(*configName)
		if err != nil {
			logger.Error("Failed to determine config path", zap.Error(err))
			os.Exit(1)
		}
	}

	if _, err := os.Stat(configPath); err != nil {
		logger.Error("Config file not found",
			zap.String("path", configPath),
			zap.Error(err))
		fmt.Fprintf(os.Stderr, "\n❌ Configuration Error:\n")
		fmt.Fprintf(os.Stderr, "Config file not found at %s\n", configPath)
		fmt.Fprintf(os.Stderr, "\nGenerate it with one of:\n")
		fmt.Fprintf(os.Stderr, "  orama config init --type node\n")
		fmt.Fprintf(os.Stderr, "  orama config init --type node --peers '<peer_multiaddr>'\n")
		os.Exit(1)
	}
}

// startNode starts the node with the given configuration and port
func startNode(ctx context.Context, cfg *config.Config, port int) error {
	logger := setup_logger(logging.ComponentNode)

	n, err := node.NewNode(cfg)
	if err != nil {
		logger.Error("failed to create node: %v", zap.Error(err))
		return err
	}

	if err := n.Start(ctx); err != nil {
		logger.Error("failed to start node: %v", zap.Error(err))
		return err
	}

	// Expand data directory path for peer.info file
	dataDir := os.ExpandEnv(cfg.Node.DataDir)
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			logger.Error("failed to determine home directory: %v", zap.Error(err))
			dataDir = cfg.Node.DataDir
		} else {
			dataDir = filepath.Join(home, dataDir[1:])
		}
	}

	// Save the peer ID to a file for CLI access
	peerID := n.GetPeerID()
	peerInfoFile := filepath.Join(dataDir, "peer.info")

	// Extract advertise IP from config (prefer http_adv_address, fallback to raft_adv_address)
	advertiseIP := "0.0.0.0" // Default fallback
	if cfg.Discovery.HttpAdvAddress != "" {
		if host, _, err := net.SplitHostPort(cfg.Discovery.HttpAdvAddress); err == nil && host != "" && host != "localhost" {
			advertiseIP = host
		}
	} else if cfg.Discovery.RaftAdvAddress != "" {
		if host, _, err := net.SplitHostPort(cfg.Discovery.RaftAdvAddress); err == nil && host != "" && host != "localhost" {
			advertiseIP = host
		}
	}

	// Determine IP protocol (IPv4 or IPv6) for multiaddr
	ipProtocol := "ip4"
	if ip := net.ParseIP(advertiseIP); ip != nil && ip.To4() == nil {
		ipProtocol = "ip6"
	}

	peerMultiaddr := fmt.Sprintf("/%s/%s/tcp/%d/p2p/%s", ipProtocol, advertiseIP, port, peerID)

	if err := os.WriteFile(peerInfoFile, []byte(peerMultiaddr), 0644); err != nil {
		logger.Error("Failed to save peer info: %v", zap.Error(err))
	} else {
		logger.Info("Peer info saved to: %s", zap.String("path", peerInfoFile))
		logger.Info("Peer multiaddr: %s", zap.String("path", peerMultiaddr))
	}

	logger.Info("Node started successfully")

	// Wait for context cancellation
	<-ctx.Done()

	// Stop node
	return n.Stop()
}

// apply_flag_overrides applies command line argument overrides to the config
func apply_flag_overrides(cfg *config.Config, p2pPort, rqlHTTP, rqlRaft *int, rqlJoinAddr *string, advAddr *string, dataDir *string) {
	logger := setup_logger(logging.ComponentNode)

	// Apply RQLite HTTP port override
	if *rqlHTTP != 5001 {
		cfg.Database.RQLitePort = *rqlHTTP
		logger.ComponentInfo(logging.ComponentNode, "Overriding RQLite HTTP port", zap.Int("port", *rqlHTTP))
	}

	// Apply RQLite Raft port override
	if *rqlRaft != 7001 {
		cfg.Database.RQLiteRaftPort = *rqlRaft
		logger.ComponentInfo(logging.ComponentNode, "Overriding RQLite Raft port", zap.Int("port", *rqlRaft))
	}

	// Apply P2P port override
	if *p2pPort != 4001 {
		cfg.Node.ListenAddresses = []string{
			fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", *p2pPort),
		}
		logger.ComponentInfo(logging.ComponentNode, "Overriding P2P port", zap.Int("port", *p2pPort))
	}

	// Apply RQLite join address
	if *rqlJoinAddr != "" {
		cfg.Database.RQLiteJoinAddress = *rqlJoinAddr
		logger.ComponentInfo(logging.ComponentNode, "Setting RQLite join address", zap.String("address", *rqlJoinAddr))
	}

	if *advAddr != "" {
		cfg.Discovery.HttpAdvAddress = fmt.Sprintf("%s:%d", *advAddr, cfg.Database.RQLitePort)
		cfg.Discovery.RaftAdvAddress = fmt.Sprintf("%s:%d", *advAddr, cfg.Database.RQLiteRaftPort)
	}

	if *dataDir != "" {
		cfg.Node.DataDir = *dataDir
	}
}

// printValidationErrors prints aggregated validation errors and exits.
func printValidationErrors(errs []error) {
	fmt.Fprintf(os.Stderr, "\nConfiguration errors (%d):\n", len(errs))
	for _, err := range errs {
		fmt.Fprintf(os.Stderr, "  - %s\n", err)
	}
	fmt.Fprintf(os.Stderr, "\nPlease fix the configuration and try again.\n")
	os.Exit(1)
}

// ensureDataDirectories ensures that all necessary data directories exist and have correct permissions.
func ensureDataDirectories(cfg *config.Config, logger *logging.ColoredLogger) error {
	// Expand ~ in data_dir path
	dataDir := os.ExpandEnv(cfg.Node.DataDir)
	if strings.HasPrefix(dataDir, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, dataDir[1:])
	}

	// Ensure Node.DataDir exists and is writable
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory %s: %w", dataDir, err)
	}
	logger.ComponentInfo(logging.ComponentNode, "Data directory created/verified", zap.String("path", dataDir))

	// Ensure RQLite data directory exists
	rqliteDir := filepath.Join(dataDir, "rqlite")
	if err := os.MkdirAll(rqliteDir, 0755); err != nil {
		return fmt.Errorf("failed to create rqlite data directory: %w", err)
	}
	logger.ComponentInfo(logging.ComponentNode, "RQLite data directory created/verified", zap.String("path", rqliteDir))

	return nil
}

func main() {
	logger := setup_logger(logging.ComponentNode)

	// Parse command-line flags
	configName, help := parse_flags()

	check_if_should_open_help(help)

	// Check if config file exists and determine path
	select_data_dir_check(configName)

	// Determine config path (handle both absolute and relative paths)
	// Note: select_data_dir_check already validated the path exists, so we can safely determine it here
	var configPath string
	var err error
	if filepath.IsAbs(*configName) {
		// Absolute path passed directly (e.g., from systemd service)
		configPath = *configName
	} else {
		// Relative path - use DefaultPath which checks both ~/.orama/configs/ and ~/.orama/
		configPath, err = config.DefaultPath(*configName)
		if err != nil {
			logger.Error("Failed to determine config path", zap.Error(err))
			fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
			os.Exit(1)
		}
	}

	var cfg *config.Config
	var cfgErr error
	cfg, cfgErr = LoadConfigFromYAML(configPath)
	if cfgErr != nil {
		logger.Error("Failed to load config from YAML", zap.Error(cfgErr))
		fmt.Fprintf(os.Stderr, "Configuration load error: %v\n", cfgErr)
		os.Exit(1)
	}
	logger.ComponentInfo(logging.ComponentNode, "Configuration loaded from YAML file", zap.String("path", configPath))

	// Set default advertised addresses if empty
	if cfg.Discovery.HttpAdvAddress == "" {
		cfg.Discovery.HttpAdvAddress = fmt.Sprintf("localhost:%d", cfg.Database.RQLitePort)
	}
	if cfg.Discovery.RaftAdvAddress == "" {
		cfg.Discovery.RaftAdvAddress = fmt.Sprintf("localhost:%d", cfg.Database.RQLiteRaftPort)
	}

	// Validate configuration
	if errs := cfg.Validate(); len(errs) > 0 {
		printValidationErrors(errs)
	}

	// Expand and create data directories
	if err := ensureDataDirectories(cfg, logger); err != nil {
		logger.Error("Failed to create data directories", zap.Error(err))
		fmt.Fprintf(os.Stderr, "\n❌ Data Directory Error:\n")
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	logger.ComponentInfo(logging.ComponentNode, "Node configuration summary",
		zap.Strings("listen_addresses", cfg.Node.ListenAddresses),
		zap.Int("rqlite_http_port", cfg.Database.RQLitePort),
		zap.Int("rqlite_raft_port", cfg.Database.RQLiteRaftPort),
		zap.Strings("peers", cfg.Discovery.BootstrapPeers),
		zap.String("rqlite_join_address", cfg.Database.RQLiteJoinAddress),
		zap.String("data_directory", cfg.Node.DataDir))

	// Extract P2P port from listen addresses
	p2pPort := 4001 // default
	if len(cfg.Node.ListenAddresses) > 0 {
		// Parse port from multiaddr like "/ip4/0.0.0.0/tcp/4001"
		parts := strings.Split(cfg.Node.ListenAddresses[0], "/")
		for i, part := range parts {
			if part == "tcp" && i+1 < len(parts) {
				if port, err := strconv.Atoi(parts[i+1]); err == nil {
					p2pPort = port
					break
				}
			}
		}
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start node in a goroutine
	errChan := make(chan error, 1)
	doneChan := make(chan struct{})
	go func() {
		if err := startNode(ctx, cfg, p2pPort); err != nil {
			errChan <- err
		}
		close(doneChan)
	}()

	// Wait for interrupt signal or error
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errChan:
		logger.ComponentError(logging.ComponentNode, "Failed to start node", zap.Error(err))
		os.Exit(1)
	case <-c:
		logger.ComponentInfo(logging.ComponentNode, "Shutting down node...")
		cancel()
		// Wait for node goroutine to finish cleanly
		<-doneChan
		logger.ComponentInfo(logging.ComponentNode, "Node shutdown complete")
	}
}
