package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
func parse_flags() (configPath, dataDir, nodeID *string, p2pPort, rqlHTTP, rqlRaft *int, rqlJoinAddr, advAddr *string, help *bool) {
	configPath = flag.String("config", "", "Path to config YAML file (overrides defaults)")
	dataDir = flag.String("data", "", "Data directory (auto-detected if not provided)")
	nodeID = flag.String("id", "", "Node identifier (for running multiple local nodes)")
	p2pPort = flag.Int("p2p-port", 4001, "LibP2P listen port")
	rqlHTTP = flag.Int("rqlite-http-port", 5001, "RQLite HTTP API port")
	rqlRaft = flag.Int("rqlite-raft-port", 7001, "RQLite Raft port")
	rqlJoinAddr = flag.String("rqlite-join-address", "", "RQLite address to join (e.g., /ip4/)")
	advAddr = flag.String("adv-addr", "127.0.0.1", "Default Advertise address for rqlite and rafts")
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

// select_data_dir selects the data directory for the node
// If none of (hasConfigFile, nodeID, dataDir) are present, throw an error and do not start
func select_data_dir(dataDir *string, nodeID *string, hasConfigFile bool) {
	logger := setup_logger(logging.ComponentNode)

	if !hasConfigFile && (*nodeID == "" || nodeID == nil) && (*dataDir == "" || dataDir == nil) {
		logger.Error("No config file, node ID, or data directory specified. Please provide at least one. Refusing to start.")
		os.Exit(1)
	}

	if *dataDir != "" {
		logger.Info("Data directory selected: %s", zap.String("dataDir", *dataDir))
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

	// Save the peer ID to a file for CLI access (especially useful for bootstrap)
	peerID := n.GetPeerID()
	peerInfoFile := filepath.Join(cfg.Node.DataDir, "peer.info")
	peerMultiaddr := fmt.Sprintf("/ip4/0.0.0.0/tcp/%d/p2p/%s", port, peerID)

	if err := os.WriteFile(peerInfoFile, []byte(peerMultiaddr), 0644); err != nil {
		logger.Error("Failed to save peer info: %v", zap.Error(err))
	} else {
		logger.Info("Peer info saved to: %s", zap.String("path", peerInfoFile))
		logger.Info("Bootstrap multiaddr: %s", zap.String("path", peerMultiaddr))
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

func main() {
	logger := setup_logger(logging.ComponentNode)

	// Parse command-line flags
	configPath, dataDir, nodeID, p2pPort, rqlHTTP, rqlRaft, rqlJoinAddr, advAddr, help := parse_flags()

	check_if_should_open_help(help)
	select_data_dir(dataDir, nodeID, *configPath != "")

	// Load configuration
	var cfg *config.Config
	if *configPath != "" {
		// Load from YAML with strict decoding
		var err error
		cfg, err = LoadConfigFromYAML(*configPath)
		if err != nil {
			logger.Error("Failed to load config from YAML", zap.Error(err))
			fmt.Fprintf(os.Stderr, "Configuration load error: %v\n", err)
			os.Exit(1)
		}
		logger.ComponentInfo(logging.ComponentNode, "Configuration loaded from YAML file", zap.String("path", *configPath))
	} else {
		// Use default configuration
		cfg = config.DefaultConfig()
		logger.ComponentInfo(logging.ComponentNode, "Default configuration loaded successfully")
	}

	// Apply command-line flag overrides
	apply_flag_overrides(cfg, p2pPort, rqlHTTP, rqlRaft, rqlJoinAddr, advAddr, dataDir)
	logger.ComponentInfo(logging.ComponentNode, "Command line arguments applied to configuration")

	// Validate configuration
	if errs := cfg.Validate(); len(errs) > 0 {
		printValidationErrors(errs)
	}

	// LibP2P uses configurable port (default 4001); RQLite uses 5001 (HTTP) and 7001 (Raft)
	port := *p2pPort

	logger.ComponentInfo(logging.ComponentNode, "Node configuration summary",
		zap.Strings("listen_addresses", cfg.Node.ListenAddresses),
		zap.Int("rqlite_http_port", cfg.Database.RQLitePort),
		zap.Int("rqlite_raft_port", cfg.Database.RQLiteRaftPort),
		zap.Int("p2p_port", port),
		zap.Strings("bootstrap_peers", cfg.Discovery.BootstrapPeers),
		zap.String("rqlite_join_address", cfg.Database.RQLiteJoinAddress),
		zap.String("data_directory", cfg.Node.DataDir))

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start node in a goroutine
	errChan := make(chan error, 1)
	doneChan := make(chan struct{})
	go func() {
		if err := startNode(ctx, cfg, port); err != nil {
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
