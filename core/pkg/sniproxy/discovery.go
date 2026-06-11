package sniproxy

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/turn"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// DefaultDiscoveryRescanInterval is the default cadence at which the TURN route
// discoverer rescans the namespaces directory. SNI route changes (a namespace
// gaining or losing its TURNS listener) are infrequent, so 30s of detection
// latency is acceptable and keeps load on the filesystem negligible.
const DefaultDiscoveryRescanInterval = 30 * time.Second

// turnConfigGlob matches the per-node TURN config files the namespace spawner
// writes under "<namespaces_dir>/<namespace>/configs/turn-<nodeID>.yaml".
const turnConfigGlob = "configs/turn-*.yaml"

// stealthBackendNamePrefix labels discovered TURN backends in logs/metrics.
const stealthBackendNamePrefix = "turn-stealth-"

// turnBackendStealthHostLabel and turnBackendNamespaceLabel are the two SNI
// hostname shapes the router forwards to a namespace's TURNS listener.
//   - the bland hashed host from turn.StealthHostForNamespace (DPI-resistant)
//   - a human-readable "turn.ns-<namespace>.<base_domain>" alias (operator UX)

// TURNDiscoveryConfig configures the namespaces scan that derives per-namespace
// stealth-TURN routes. All fields are required; a zero RescanInterval selects
// DefaultDiscoveryRescanInterval.
type TURNDiscoveryConfig struct {
	// NamespacesDir is the directory holding one subdirectory per namespace,
	// each containing a "configs/turn-*.yaml" written by the namespace spawner
	// (e.g. "/opt/orama/.orama/data/namespaces").
	NamespacesDir string `yaml:"namespaces_dir"`

	// BaseDomain is the cluster's base domain (e.g. "orama-devnet.network"),
	// used to derive the stealth and "turn.ns-*" SNI hostnames.
	BaseDomain string `yaml:"base_domain"`

	// RescanInterval is how often the namespaces directory is rescanned. Zero
	// selects DefaultDiscoveryRescanInterval.
	RescanInterval time.Duration `yaml:"rescan_interval"`
}

// Validate reports configuration errors. It does not touch the filesystem; a
// missing NamespacesDir at scan time is a transient error handled by the
// discoverer (previous routes are kept), not a config error.
func (c *TURNDiscoveryConfig) Validate() []string {
	var errs []string
	if c.NamespacesDir == "" {
		errs = append(errs, "turn_discovery.namespaces_dir: required")
	}
	if c.BaseDomain == "" {
		errs = append(errs, "turn_discovery.base_domain: required")
	}
	return errs
}

// DiscoverTURNRoutes scans cfg.NamespacesDir for per-namespace TURN configs and
// returns two routes per namespace that exposes a TURNS listener:
//
//   - turn.StealthHostForNamespace(namespace, baseDomain) -> 127.0.0.1:<tls-port>
//   - "turn.ns-<namespace>.<baseDomain>"                  -> 127.0.0.1:<tls-port>
//
// Namespaces whose TURN config has an empty turns_listen_addr (TURNS disabled)
// are skipped. A turn-*.yaml that cannot be read or parsed is skipped with a
// per-file warning, but the scan continues for the rest — one bad file must not
// hide every other namespace's routes.
//
// A failure to read the namespaces directory itself returns an error so callers
// can keep the previously-installed routes rather than wiping them on a
// transient filesystem error.
func DiscoverTURNRoutes(cfg TURNDiscoveryConfig, logger *zap.Logger) ([]Route, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	entries, err := os.ReadDir(cfg.NamespacesDir)
	if err != nil {
		return nil, fmt.Errorf("read namespaces dir %s: %w", cfg.NamespacesDir, err)
	}

	var routes []Route
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		nsRoutes := discoverNamespaceRoutes(cfg, entry.Name(), logger)
		routes = append(routes, nsRoutes...)
	}

	// Deterministic order keeps Router.Replace idempotent and tests stable.
	sort.Slice(routes, func(i, j int) bool { return routes[i].Match < routes[j].Match })
	return routes, nil
}

// discoverNamespaceRoutes resolves the stealth + alias routes for a single
// namespace directory. Returns nil when the namespace has no TURNS listener or
// its config is unreadable/unparseable (logged, not fatal).
func discoverNamespaceRoutes(cfg TURNDiscoveryConfig, nsDir string, logger *zap.Logger) []Route {
	glob := filepath.Join(cfg.NamespacesDir, nsDir, turnConfigGlob)
	matches, err := filepath.Glob(glob)
	if err != nil {
		// Glob only errors on a malformed pattern, which turnConfigGlob is not;
		// guard anyway so a future edit can't silently swallow it.
		logger.Warn("turn-config glob failed",
			zap.String("namespace_dir", nsDir), zap.Error(err))
		return nil
	}

	for _, configPath := range matches {
		namespace, tlsPort, ok := parseTURNConfig(configPath, logger)
		if !ok {
			continue
		}
		backend := Backend{
			Name:    stealthBackendNamePrefix + namespace,
			Network: "tcp",
			Addr:    net.JoinHostPort("127.0.0.1", tlsPort),
		}
		return []Route{
			{Match: turn.StealthHostForNamespace(namespace, cfg.BaseDomain), Backend: backend},
			{Match: fmt.Sprintf("turn.ns-%s.%s", namespace, cfg.BaseDomain), Backend: backend},
		}
	}
	return nil
}

// parseTURNConfig reads a turn-*.yaml and returns its namespace and TURNS port.
// ok is false (with a warning) when the file is unreadable/unparseable, when it
// names no namespace, or when TURNS is disabled (empty turns_listen_addr).
func parseTURNConfig(path string, logger *zap.Logger) (namespace, tlsPort string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		logger.Warn("read turn config failed", zap.String("path", path), zap.Error(err))
		return "", "", false
	}

	var c turn.Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		logger.Warn("parse turn config failed", zap.String("path", path), zap.Error(err))
		return "", "", false
	}

	if c.Namespace == "" {
		logger.Warn("turn config has empty namespace", zap.String("path", path))
		return "", "", false
	}
	if strings.TrimSpace(c.TURNSListenAddr) == "" {
		// TURNS disabled for this namespace — no stealth route, not an error.
		return "", "", false
	}

	port, err := portFromListenAddr(c.TURNSListenAddr)
	if err != nil {
		logger.Warn("turn config has invalid turns_listen_addr",
			zap.String("path", path),
			zap.String("turns_listen_addr", c.TURNSListenAddr),
			zap.Error(err))
		return "", "", false
	}
	return c.Namespace, port, true
}

// portFromListenAddr extracts the port from a "host:port" TURNS listen address
// (e.g. "0.0.0.0:5349" -> "5349"). The router always dials 127.0.0.1, so only
// the port is needed.
func portFromListenAddr(addr string) (string, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("split host:port: %w", err)
	}
	if port == "" {
		return "", fmt.Errorf("empty port in %q", addr)
	}
	return port, nil
}
