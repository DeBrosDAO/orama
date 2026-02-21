package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
)

func parseTURNConfig(logger *logging.ColoredLogger) *turn.Config {
	configFlag := flag.String("config", "", "Config file path (absolute path or filename in ~/.orama)")
	flag.Parse()

	var configPath string
	var err error
	if *configFlag != "" {
		if filepath.IsAbs(*configFlag) {
			configPath = *configFlag
		} else {
			configPath, err = config.DefaultPath(*configFlag)
			if err != nil {
				logger.ComponentError(logging.ComponentTURN, "Failed to determine config path", zap.Error(err))
				fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		configPath, err = config.DefaultPath("turn.yaml")
		if err != nil {
			logger.ComponentError(logging.ComponentTURN, "Failed to determine config path", zap.Error(err))
			fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
			os.Exit(1)
		}
	}

	type yamlCfg struct {
		ListenAddr     string `yaml:"listen_addr"`
		TLSListenAddr  string `yaml:"tls_listen_addr"`
		PublicIP       string `yaml:"public_ip"`
		Realm          string `yaml:"realm"`
		AuthSecret     string `yaml:"auth_secret"`
		RelayPortStart int    `yaml:"relay_port_start"`
		RelayPortEnd   int    `yaml:"relay_port_end"`
		Namespace      string `yaml:"namespace"`
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		logger.ComponentError(logging.ComponentTURN, "Config file not found",
			zap.String("path", configPath), zap.Error(err))
		fmt.Fprintf(os.Stderr, "\nConfig file not found at %s\n", configPath)
		os.Exit(1)
	}

	var y yamlCfg
	if err := config.DecodeStrict(strings.NewReader(string(data)), &y); err != nil {
		logger.ComponentError(logging.ComponentTURN, "Failed to parse TURN config", zap.Error(err))
		fmt.Fprintf(os.Stderr, "Configuration parse error: %v\n", err)
		os.Exit(1)
	}

	cfg := &turn.Config{
		ListenAddr:     y.ListenAddr,
		TLSListenAddr:  y.TLSListenAddr,
		PublicIP:       y.PublicIP,
		Realm:          y.Realm,
		AuthSecret:     y.AuthSecret,
		RelayPortStart: y.RelayPortStart,
		RelayPortEnd:   y.RelayPortEnd,
		Namespace:      y.Namespace,
	}

	if errs := cfg.Validate(); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\nTURN configuration errors (%d):\n", len(errs))
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		fmt.Fprintf(os.Stderr, "\nPlease fix the configuration and try again.\n")
		os.Exit(1)
	}

	logger.ComponentInfo(logging.ComponentTURN, "Loaded TURN configuration",
		zap.String("path", configPath),
		zap.String("listen_addr", cfg.ListenAddr),
		zap.String("namespace", cfg.Namespace),
		zap.String("realm", cfg.Realm),
	)

	return cfg
}
