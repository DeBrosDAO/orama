package config

import (
	"fmt"
	"net"
	"os"

	"gopkg.in/yaml.v3"
)

// InstalledOlricURL is the index Olric HTTP API of the node that wrote the node config at path
// (ProductionNodeConfigPath on an installed node): the first of http_gateway.olric_servers. Olric
// binds the node's WireGuard address, never loopback, so on-node tooling reads the address the
// installer configured instead of assuming localhost.
func InstalledOlricURL(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read node config %s (run this on an installed node, as root): %w", path, err)
	}
	var cfg struct {
		HTTPGateway struct {
			OlricServers []string `yaml:"olric_servers"`
		} `yaml:"http_gateway"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse node config %s: %w", path, err)
	}
	if len(cfg.HTTPGateway.OlricServers) == 0 {
		return "", fmt.Errorf("node config %s has no http_gateway.olric_servers", path)
	}
	addr := cfg.HTTPGateway.OlricServers[0]
	if host, port, err := net.SplitHostPort(addr); err != nil || host == "" || port == "" {
		return "", fmt.Errorf("node config %s: olric server %q is not host:port", path, addr)
	}
	return "http://" + addr, nil
}
