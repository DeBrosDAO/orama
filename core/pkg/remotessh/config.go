package remotessh

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/inspector"
)

// FindNodesConf locates nodes.conf, the legacy node inventory.
//
// The inventory of record is the network API, read through
// noderesolver.ResolveNodes; nodes.conf is only what that falls back to when
// the API is unreachable or the operator has not logged in yet. Its stable
// home is ~/.orama/nodes.conf. The three relative paths are a source-tree
// convenience and depend on the working directory, so no command may resolve
// nodes through them by default.
func FindNodesConf() string {
	candidates := []string{
		"scripts/nodes.conf",
		"../scripts/nodes.conf",
		"network/scripts/nodes.conf",
	}

	// Also check from home dir
	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".orama", "nodes.conf"))
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// LoadEnvNodes loads all nodes for a given environment from nodes.conf.
// SSHKey fields are NOT set — caller must call PrepareNodeKeys() after this.
//
// Call noderesolver.ResolveNodes instead: it asks the network first and calls
// this only as its fallback. Commands that read nodes.conf directly saw a
// different fleet than the ones that did not.
func LoadEnvNodes(env string) ([]inspector.Node, error) {
	confPath := FindNodesConf()
	if confPath == "" {
		return nil, fmt.Errorf("nodes.conf not found (checked ~/.orama/nodes.conf, scripts/, ../scripts/, network/scripts/)")
	}

	nodes, err := inspector.LoadNodes(confPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load %s: %w", confPath, err)
	}

	filtered := inspector.FilterByEnv(nodes, env)
	if len(filtered) == 0 {
		return nil, fmt.Errorf("no nodes found for environment %q in %s", env, confPath)
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
