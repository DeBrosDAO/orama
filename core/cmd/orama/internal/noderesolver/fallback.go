package noderesolver

import (
	"fmt"

	"github.com/DeBrosOfficial/network/cmd/orama/internal"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
)

// chooseNodes is the order ResolveNodes uses.
//
// The gateway inventory wins when it answers with nodes. Before the
// cluster's name resolves, that request fails, and the machines setup
// recorded on the environment are what an operator can still reach. nodes.conf
// is the last resort, for a fleet that was written down by hand.
func chooseNodes(api []inspector.Node, apiErr error, recorded, conf []inspector.Node, confErr error) ([]inspector.Node, error) {
	if apiErr == nil && len(api) > 0 {
		return api, nil
	}
	if len(recorded) > 0 {
		return recorded, nil
	}
	if confErr == nil && len(conf) > 0 {
		return conf, nil
	}
	switch {
	case apiErr != nil && confErr != nil:
		return nil, fmt.Errorf("network API: %w; nodes.conf: %v", apiErr, confErr)
	case apiErr != nil:
		return nil, apiErr
	case confErr != nil:
		return nil, confErr
	default:
		return nil, fmt.Errorf("no nodes are recorded for this environment")
	}
}

// recordedNodes reads the machines setup stored on the environment. A missing
// environment is not an error: the caller still has the API and nodes.conf.
func recordedNodes(env string) []inspector.Node {
	if env == "" {
		return nil
	}
	e, err := cli.GetEnvironmentByName(env)
	if err != nil || e == nil {
		return nil
	}
	nodes := make([]inspector.Node, 0, len(e.Nodes))
	for _, n := range e.Nodes {
		node := NewNode(n.Host, n.User, e.Name)
		node.Role = n.Role
		nodes = append(nodes, node)
	}
	return nodes
}

func loadConfNodes(env string) ([]inspector.Node, error) {
	return remotessh.LoadEnvNodes(env)
}
