// Command sni-router is a TLS-level Server Name Indication router.
//
// It listens on a public TCP port (typically :443), peeks at the TLS
// ClientHello SNI on each connection, and forwards the raw stream to
// a configured backend. It does NOT terminate TLS — encrypted bytes
// pass through verbatim. This lets one port serve multiple TLS-speaking
// backends (HTTPS for the gateway, TURN-over-TLS for stealth WebRTC).
//
// See pkg/sniproxy for the underlying library.
//
// Configuration: YAML file at --config (defaults to ~/.orama/sni-router.yaml).
//
// Example sni-router.yaml:
//
//	listen: ":443"
//	client_hello_timeout: 5s
//	backend_dial_timeout: 5s
//	max_concurrent_conns: 10000
//	fallback:
//	  name: caddy
//	  addr: "127.0.0.1:8443"
//	routes:
//	  - match: "cdn.example.com"
//	    backend:
//	      name: turn-tls
//	      addr: "127.0.0.1:5349"
//	  - match: "turn.example.com"
//	    backend:
//	      name: turn-tls
//	      addr: "127.0.0.1:5349"
//	  - match: "*.ns-myapp.example.com"
//	    backend:
//	      name: gateway
//	      addr: "127.0.0.1:8443"
//	turn_discovery:
//	  namespaces_dir: /opt/orama/.orama/data/namespaces
//	  base_domain: orama-devnet.network
//	  rescan_interval: 30s
//
// When the turn_discovery.namespaces_dir is set, the router additionally scans
// <namespaces_dir>/*/configs/turn-*.yaml every rescan_interval and derives two
// routes per namespace with a TURNS listener — the bland stealth host and a
// "turn.ns-<namespace>.<base_domain>" alias — both forwarding to that
// namespace's local TURNS port. Discovered routes are merged with the static
// routes above (static wins on conflict); a transient scan error keeps the
// previously-installed routes.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/sniproxy"
	"go.uber.org/zap"
)

var (
	version = "dev"
	commit  = "unknown"
)

// yamlBackend mirrors sniproxy.Backend for YAML decoding.
type yamlBackend struct {
	Name    string `yaml:"name"`
	Network string `yaml:"network"`
	Addr    string `yaml:"addr"`
}

// yamlRoute mirrors sniproxy.Route for YAML decoding.
type yamlRoute struct {
	Match   string      `yaml:"match"`
	Backend yamlBackend `yaml:"backend"`
}

// yamlTURNDiscovery mirrors sniproxy.TURNDiscoveryConfig for YAML decoding.
// When present and namespaces_dir is set, the router auto-discovers per-
// namespace stealth-TURN routes by scanning <namespaces_dir>/*/configs/turn-*.yaml.
type yamlTURNDiscovery struct {
	NamespacesDir  string        `yaml:"namespaces_dir"`
	BaseDomain     string        `yaml:"base_domain"`
	RescanInterval time.Duration `yaml:"rescan_interval"`
}

// yamlConfig is the on-disk configuration shape.
type yamlConfig struct {
	Listen             string            `yaml:"listen"`
	ClientHelloTimeout time.Duration     `yaml:"client_hello_timeout"`
	BackendDialTimeout time.Duration     `yaml:"backend_dial_timeout"`
	MaxConcurrentConns int               `yaml:"max_concurrent_conns"`
	Fallback           yamlBackend       `yaml:"fallback"`
	Routes             []yamlRoute       `yaml:"routes"`
	TURNDiscovery      yamlTURNDiscovery `yaml:"turn_discovery"`
}

// discoveryEnabled reports whether TURN route auto-discovery is configured.
func (y *yamlConfig) discoveryEnabled() bool {
	return y.TURNDiscovery.NamespacesDir != ""
}

func main() {
	logger, err := logging.NewColoredLogger(logging.ComponentSNI, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}

	logger.ComponentInfo(logging.ComponentSNI, "Starting SNI router",
		zap.String("version", version),
		zap.String("commit", commit))

	cfg, configPath := parseConfig(logger)

	router := sniproxy.NewRouter(toBackend(cfg.Fallback))

	// The static routes (and fallback) always come from the config file; this
	// closure is re-evaluated on every reload/rescan so a hand-edit to the
	// config is picked up without a restart.
	staticSource := func() ([]sniproxy.Route, sniproxy.Backend, error) {
		y, err := loadConfig(configPath)
		if err != nil {
			return nil, sniproxy.Backend{}, err
		}
		return toRoutes(y.Routes), toBackend(y.Fallback), nil
	}

	routeStop := make(chan struct{})
	defer close(routeStop)

	if cfg.discoveryEnabled() {
		// Auto-discover per-namespace stealth-TURN routes by scanning the
		// namespaces directory, merged with the static config routes (static
		// wins on conflict), re-installed atomically every rescan_interval. A
		// transient scan error keeps the previously-installed routes.
		discoverer := sniproxy.NewTURNRouteDiscoverer(
			sniproxy.TURNDiscoveryConfig{
				NamespacesDir:  cfg.TURNDiscovery.NamespacesDir,
				BaseDomain:     cfg.TURNDiscovery.BaseDomain,
				RescanInterval: cfg.TURNDiscovery.RescanInterval,
			}, staticSource, router, logger.Logger)
		if err := discoverer.Apply(); err != nil {
			logger.ComponentError(logging.ComponentSNI, "Failed to install initial routes",
				zap.Error(err))
			os.Exit(1)
		}
		go discoverer.Run(routeStop)
	} else {
		// No discovery configured: hot-reload the static route table from the
		// config file so cdn/turn SNI routes can be added or removed without
		// restarting (Router.Replace swaps atomically under in-flight conns).
		reloader := sniproxy.NewFileRouteReloader(configPath, staticSource, router, logger.Logger)
		if err := reloader.Apply(); err != nil {
			logger.ComponentError(logging.ComponentSNI, "Failed to install initial routes",
				zap.Error(err))
			os.Exit(1)
		}
		go reloader.Watch(sniproxy.DefaultRouteReloadInterval, routeStop)
	}

	srv := sniproxy.NewServer(router, sniproxy.Config{
		ClientHelloTimeout: cfg.ClientHelloTimeout,
		BackendDialTimeout: cfg.BackendDialTimeout,
		MaxConcurrentConns: cfg.MaxConcurrentConns,
	}, logger.Logger)

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		logger.ComponentError(logging.ComponentSNI, "Failed to listen",
			zap.String("addr", cfg.Listen), zap.Error(err))
		os.Exit(1)
	}

	logger.ComponentInfo(logging.ComponentSNI, "SNI router listening",
		zap.String("addr", cfg.Listen),
		zap.Int("routes", len(cfg.Routes)),
		zap.String("fallback", cfg.Fallback.Addr),
	)

	// Run Serve in a goroutine so the main goroutine can wait on signals.
	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- srv.Serve(ln)
	}()

	// Wait for termination signal or unrecoverable Serve error.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.ComponentInfo(logging.ComponentSNI, "Shutdown signal received",
			zap.String("signal", sig.String()))
	case err := <-serveErrCh:
		logger.ComponentError(logging.ComponentSNI, "Serve returned",
			zap.Error(err))
	}

	// Stop accepting new connections, then drain in-flight ones.
	_ = ln.Close()
	srv.Close()

	logger.ComponentInfo(logging.ComponentSNI, "SNI router shutdown complete")
}

func parseConfig(logger *logging.ColoredLogger) (yamlConfig, string) {
	configFlag := flag.String("config", "", "Config file path (absolute or filename in ~/.orama)")
	flag.Parse()

	var configPath string
	var err error
	if *configFlag != "" {
		if filepath.IsAbs(*configFlag) {
			configPath = *configFlag
		} else {
			configPath, err = config.DefaultPath(*configFlag)
			if err != nil {
				logger.ComponentError(logging.ComponentSNI, "Failed to determine config path",
					zap.Error(err))
				os.Exit(1)
			}
		}
	} else {
		configPath, err = config.DefaultPath("sni-router.yaml")
		if err != nil {
			logger.ComponentError(logging.ComponentSNI, "Failed to determine config path",
				zap.Error(err))
			os.Exit(1)
		}
	}

	y, err := loadConfig(configPath)
	if err != nil {
		logger.ComponentError(logging.ComponentSNI, "Failed to load SNI router config",
			zap.String("path", configPath), zap.Error(err))
		fmt.Fprintf(os.Stderr, "\nSNI router configuration error: %v\n", err)
		os.Exit(1)
	}

	logger.ComponentInfo(logging.ComponentSNI, "Loaded SNI router configuration",
		zap.String("path", configPath),
	)

	return y, configPath
}

// loadConfig reads, decodes, and validates the SNI router config file. Shared
// by the initial parse and every hot-reload, so it returns an error instead of
// exiting the process.
func loadConfig(path string) (yamlConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return yamlConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var y yamlConfig
	if err := config.DecodeStrict(strings.NewReader(string(data)), &y); err != nil {
		return yamlConfig{}, fmt.Errorf("parse config: %w", err)
	}
	if errs := validateConfig(&y); len(errs) > 0 {
		return yamlConfig{}, fmt.Errorf("invalid config: %s", strings.Join(errs, "; "))
	}
	return y, nil
}

// validateConfig returns a non-empty slice of human-readable errors on misconfig.
func validateConfig(y *yamlConfig) []string {
	var errs []string
	if y.Listen == "" {
		errs = append(errs, "listen: required (e.g. \":443\")")
	}
	if y.Fallback.Addr == "" {
		errs = append(errs, "fallback.addr: required (where to send unmatched SNIs, typically Caddy)")
	}
	for i, r := range y.Routes {
		if r.Match == "" {
			errs = append(errs, fmt.Sprintf("routes[%d].match: required", i))
		}
		if r.Backend.Addr == "" {
			errs = append(errs, fmt.Sprintf("routes[%d].backend.addr: required", i))
		}
	}
	// turn_discovery is optional, but when partially set (namespaces_dir XOR
	// base_domain) it is almost certainly a misconfiguration, so validate the
	// pair together via the library's own Validate.
	if y.discoveryEnabled() || y.TURNDiscovery.BaseDomain != "" {
		dc := sniproxy.TURNDiscoveryConfig{
			NamespacesDir: y.TURNDiscovery.NamespacesDir,
			BaseDomain:    y.TURNDiscovery.BaseDomain,
		}
		errs = append(errs, dc.Validate()...)
	}
	return errs
}

func toBackend(b yamlBackend) sniproxy.Backend {
	network := b.Network
	if network == "" {
		network = "tcp"
	}
	return sniproxy.Backend{
		Name:    b.Name,
		Network: network,
		Addr:    b.Addr,
	}
}

func toRoutes(in []yamlRoute) []sniproxy.Route {
	out := make([]sniproxy.Route, len(in))
	for i, r := range in {
		out[i] = sniproxy.Route{
			Match:   r.Match,
			Backend: toBackend(r.Backend),
		}
	}
	return out
}
