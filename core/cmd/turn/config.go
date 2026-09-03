package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
)

func parseTURNConfig(logger *logging.ColoredLogger) (*turn.Config, string) {
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

	data, err := os.ReadFile(configPath)
	if err != nil {
		logger.ComponentError(logging.ComponentTURN, "Config file not found",
			zap.String("path", configPath), zap.Error(err))
		fmt.Fprintf(os.Stderr, "\nConfig file not found at %s\n", configPath)
		os.Exit(1)
	}

	cfg, err := decodeTURNConfig(data)
	if err != nil {
		logger.ComponentError(logging.ComponentTURN, "Failed to parse TURN config", zap.Error(err))
		fmt.Fprintf(os.Stderr, "Configuration parse error: %v\n", err)
		os.Exit(1)
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
		zap.Strings("tenants", tenantNamespaces(cfg)),
		zap.String("realm", cfg.Realm),
	)

	return cfg, configPath
}

// decodeTURNConfig decodes the TURN YAML the namespace spawner writes
// (yaml.Marshal of turn.Config) into a turn.Config.
//
// The decode itself lives in pkg/turn so the struct's own yaml tags are the
// single definition of the writer/reader contract. This used to be a mirror
// struct here, which meant every new field on turn.Config crashed the TURN
// binary at startup until someone remembered to duplicate it — strict decoding
// rejects unknown keys. Bugboard #283 hit exactly that with `tenants`.
// Kept as a named function so the spawner-output ↔ parser contract stays
// unit-testable (see config_test.go).
func decodeTURNConfig(data []byte) (*turn.Config, error) {
	return turn.ParseConfig(data)
}

// tenantNamespaces lists the namespaces this server is configured to serve, for
// the startup log. The shared config has no single namespace — cfg.Namespace is
// the legacy single-tenant field and is empty for it.
func tenantNamespaces(cfg *turn.Config) []string {
	tenants := cfg.ResolvedTenants()
	out := make([]string, 0, len(tenants))
	for _, t := range tenants {
		out = append(out, t.Namespace)
	}
	return out
}
