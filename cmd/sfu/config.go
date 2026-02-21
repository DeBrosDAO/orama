package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/sfu"
	"go.uber.org/zap"
)

// newSFUServer creates a new SFU server from config and logger.
// Wrapper to keep main.go clean and avoid importing sfu in main.
func newSFUServer(cfg *sfu.Config, logger *zap.Logger) (*sfu.Server, error) {
	return sfu.NewServer(cfg, logger)
}

func parseSFUConfig(logger *logging.ColoredLogger) *sfu.Config {
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
				logger.ComponentError(logging.ComponentSFU, "Failed to determine config path", zap.Error(err))
				fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		configPath, err = config.DefaultPath("sfu.yaml")
		if err != nil {
			logger.ComponentError(logging.ComponentSFU, "Failed to determine config path", zap.Error(err))
			fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
			os.Exit(1)
		}
	}

	type yamlTURNServer struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	}

	type yamlCfg struct {
		ListenAddr        string           `yaml:"listen_addr"`
		Namespace         string           `yaml:"namespace"`
		MediaPortStart    int              `yaml:"media_port_start"`
		MediaPortEnd      int              `yaml:"media_port_end"`
		TURNServers       []yamlTURNServer `yaml:"turn_servers"`
		TURNSecret        string           `yaml:"turn_secret"`
		TURNCredentialTTL int              `yaml:"turn_credential_ttl"`
		RQLiteDSN         string           `yaml:"rqlite_dsn"`
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		logger.ComponentError(logging.ComponentSFU, "Config file not found",
			zap.String("path", configPath), zap.Error(err))
		fmt.Fprintf(os.Stderr, "\nConfig file not found at %s\n", configPath)
		os.Exit(1)
	}

	var y yamlCfg
	if err := config.DecodeStrict(strings.NewReader(string(data)), &y); err != nil {
		logger.ComponentError(logging.ComponentSFU, "Failed to parse SFU config", zap.Error(err))
		fmt.Fprintf(os.Stderr, "Configuration parse error: %v\n", err)
		os.Exit(1)
	}

	var turnServers []sfu.TURNServerConfig
	for _, ts := range y.TURNServers {
		turnServers = append(turnServers, sfu.TURNServerConfig{
			Host: ts.Host,
			Port: ts.Port,
		})
	}

	cfg := &sfu.Config{
		ListenAddr:        y.ListenAddr,
		Namespace:         y.Namespace,
		MediaPortStart:    y.MediaPortStart,
		MediaPortEnd:      y.MediaPortEnd,
		TURNServers:       turnServers,
		TURNSecret:        y.TURNSecret,
		TURNCredentialTTL: y.TURNCredentialTTL,
		RQLiteDSN:         y.RQLiteDSN,
	}

	if errs := cfg.Validate(); len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\nSFU configuration errors (%d):\n", len(errs))
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		fmt.Fprintf(os.Stderr, "\nPlease fix the configuration and try again.\n")
		os.Exit(1)
	}

	logger.ComponentInfo(logging.ComponentSFU, "Loaded SFU configuration",
		zap.String("path", configPath),
		zap.String("listen_addr", cfg.ListenAddr),
		zap.String("namespace", cfg.Namespace),
		zap.Int("media_ports", cfg.MediaPortEnd-cfg.MediaPortStart),
		zap.Int("turn_servers", len(cfg.TURNServers)),
	)

	return cfg
}
