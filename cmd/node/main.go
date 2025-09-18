package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/DeBrosOfficial/network/pkg/anyoneproxy"
	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/node"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// setup_logger initializes a logger for the given component.
func setup_logger(component logging.Component) (logger *logging.ColoredLogger) {
	var err error

	logger, err = logging.NewColoredLogger(component, true)
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}

	return logger
}

// parse_and_return_network_flags it initializes all the network flags coming from the .yaml files
func parse_and_return_network_flags() (configPath *string, dataDir, nodeID *string, p2pPort, rqlHTTP, rqlRaft *int, disableAnon *bool, rqlJoinAddr *string, advAddr *string, help *bool) {
	logger := setup_logger(logging.ComponentNode)

	configPath = flag.String("config", "", "Path to config YAML file (overrides defaults)")
	dataDir = flag.String("data", "", "Data directory (auto-detected if not provided)")
	nodeID = flag.String("id", "", "Node identifier (for running multiple local nodes)")
	p2pPort = flag.Int("p2p-port", 4001, "LibP2P listen port")
	rqlHTTP = flag.Int("rqlite-http-port", 5001, "RQLite HTTP API port")
	rqlRaft = flag.Int("rqlite-raft-port", 7001, "RQLite Raft port")
	disableAnon = flag.Bool("disable-anonrc", false, "Disable Anyone proxy routing (defaults to enabled on 127.0.0.1:9050)")
	rqlJoinAddr = flag.String("rqlite-join-address", "", "RQLite address to join (e.g., /ip4/)")
	advAddr = flag.String("adv-addr", "127.0.0.1", "Default Advertise address for rqlite and rafts")
	help = flag.Bool("help", false, "Show help")
	flag.Parse()

	logger.Info("Successfully parsed all flags and arguments.")

	if *configPath != "" {
		cfg, err := LoadConfigFromYAML(*configPath)
		if err != nil {
			logger.Error("Failed to load config from YAML", zap.Error(err))
			os.Exit(1)
		}
		logger.ComponentInfo(logging.ComponentNode, "Configuration loaded from YAML file", zap.String("path", *configPath))

		// Instead of returning flag values, return config values
		// For ListenAddresses, extract port from multiaddr string if possible, else use default
		var p2pPortVal int
		if len(cfg.Node.ListenAddresses) > 0 {
			// Try to parse port from multiaddr string
			var port int
			_, err := fmt.Sscanf(cfg.Node.ListenAddresses[0], "/ip4/0.0.0.0/tcp/%d", &port)
			if err == nil {
				p2pPortVal = port
			} else {
				p2pPortVal = 4001
			}
		} else {
			p2pPortVal = 4001
		}
		return configPath,
			&cfg.Node.DataDir,
			&cfg.Node.ID,
			&p2pPortVal,
			&cfg.Database.RQLitePort,
			&cfg.Database.RQLiteRaftPort,
			&cfg.Node.DisableAnonRC,
			&cfg.Database.RQLiteJoinAddress,
			&cfg.Discovery.HttpAdvAddress,
			help
	}

	return
}

// LoadConfigFromYAML loads a config from a YAML file
func LoadConfigFromYAML(path string) (*config.Config, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}
	return &cfg, nil
}

// disable_anon_proxy disables the anonymous proxy routing, by default on development
// it is not suggested to run anyone proxy
func disable_anon_proxy(disableAnon *bool) bool {
	anyoneproxy.SetDisabled(*disableAnon)
	logger := setup_logger(logging.ComponentAnyone)

	if *disableAnon {
		logger.Info("Anyone proxy routing is disabled. This means the node will not use the default Tor proxy for anonymous routing.\n")
	}

	return true
}

// check_if_should_open_help checks if the help flag is set and opens the help if it is
func check_if_should_open_help(help *bool) {
	if *help {
		flag.Usage()
		return
	}
}

// select_data_dir selects the data directory for the node
func select_data_dir(dataDir *string, nodeID *string) {
	logger := setup_logger(logging.ComponentNode)

	if *nodeID == "" {
		*dataDir = "./data/node"
	}

	logger.Info("Successfully selected Data Directory of: %s", zap.String("dataDir", *dataDir))
}

// startNode starts the node with the given configuration and port
func startNode(ctx context.Context, cfg *config.Config, port int) error {
	logger := setup_logger(logging.ComponentNode)

	n, err := node.NewNode(cfg)
	if err != nil {
		logger.Error("failed to create node: %v", zap.Error(err))
	}

	if err := n.Start(ctx); err != nil {
		logger.Error("failed to start node: %v", zap.Error(err))
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

// load_args_into_config applies command line argument overrides to the config
func load_args_into_config(cfg *config.Config, p2pPort, rqlHTTP, rqlRaft *int, rqlJoinAddr *string, advAddr *string, dataDir *string) {
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
		cfg.Discovery.HttpAdvAddress = fmt.Sprintf("%s:%d", *advAddr, *rqlHTTP)
		cfg.Discovery.RaftAdvAddress = fmt.Sprintf("%s:%d", *advAddr, *rqlRaft)
	}

	if *dataDir != "" {
		cfg.Node.DataDir = *dataDir
	}
}

func main() {
	logger := setup_logger(logging.ComponentNode)

	_, dataDir, nodeID, p2pPort, rqlHTTP, rqlRaft, disableAnon, rqlJoinAddr, advAddr, help := parse_and_return_network_flags()

	disable_anon_proxy(disableAnon)
	check_if_should_open_help(help)
	select_data_dir(dataDir, nodeID)

	// Load Node Configuration
	var cfg *config.Config
	cfg = config.DefaultConfig()
	logger.ComponentInfo(logging.ComponentNode, "Default configuration loaded successfully")

	// Apply command line argument overrides
	load_args_into_config(cfg, p2pPort, rqlHTTP, rqlRaft, rqlJoinAddr, advAddr, dataDir)
	logger.ComponentInfo(logging.ComponentNode, "Command line arguments applied to configuration")

	// LibP2P uses configurable port (default 4001); RQLite uses 5001 (HTTP) and 7001 (Raft)
	port := *p2pPort

	logger.ComponentInfo(logging.ComponentNode, "Node configuration summary",
		zap.Strings("listen_addresses", cfg.Node.ListenAddresses),
		zap.Int("rqlite_http_port", cfg.Database.RQLitePort),
		zap.Int("rqlite_raft_port", cfg.Database.RQLiteRaftPort),
		zap.Int("p2p_port", port),
		zap.Strings("bootstrap_peers", cfg.Discovery.BootstrapPeers),
		zap.String("rqlite_join_address", cfg.Database.RQLiteJoinAddress),
		zap.String("data_directory", *dataDir))

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
