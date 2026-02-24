package remotessh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// FindRemoteNodesConf searches for the remote-nodes.conf file
// in common locations relative to the current directory or project root.
func FindRemoteNodesConf() string {
	candidates := []string{
		"scripts/remote-nodes.conf",
		"../scripts/remote-nodes.conf",
		"network/scripts/remote-nodes.conf",
	}

	// Also check from home dir
	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".orama", "remote-nodes.conf"))
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// LoadEnvNodes loads all nodes for a given environment from remote-nodes.conf.
func LoadEnvNodes(env string) ([]inspector.Node, error) {
	confPath := FindRemoteNodesConf()
	if confPath == "" {
		return nil, fmt.Errorf("remote-nodes.conf not found (checked scripts/, ../scripts/, network/scripts/)")
	}

	nodes, err := inspector.LoadNodes(confPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load %s: %w", confPath, err)
	}

	filtered := inspector.FilterByEnv(nodes, env)
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no nodes found for environment %q in %s", env, confPath)
	}

	// Expand ~ in SSH key paths
	home, _ := os.UserHomeDir()
	for i := range filtered {
		if filtered[i].SSHKey != "" && strings.HasPrefix(filtered[i].SSHKey, "~") {
			filtered[i].SSHKey = filepath.Join(home, filtered[i].SSHKey[1:])
		}
	}

	return filtered, nil
}

// PickHubNode selects the first node as the hub for fanout distribution.
func PickHubNode(nodes []inspector.Node) inspector.Node {
	return nodes[0]
}

// FilterByIP returns nodes matching the given IP address.
func FilterByIP(nodes []inspector.Node, ip string) []inspector.Node {
	var filtered []inspector.Node
	for _, n := range nodes {
		if n.Host == ip {
			filtered = append(filtered, n)
		}
	}
	return filtered
}
