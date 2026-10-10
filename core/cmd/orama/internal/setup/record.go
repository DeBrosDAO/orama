package setup

import (
	"fmt"

	cli "github.com/DeBrosOfficial/network/cmd/orama/internal"
)

// cliRecorder keeps what a run set up in the CLI's configuration
// (environments.json), where every later command reads it.
type cliRecorder struct{}

func (cliRecorder) Cluster(env, network, gateway string, nodes []RecordedNode) error {
	existing, err := cli.GetEnvironmentByName(env)
	switch {
	case err != nil || existing == nil:
		if gateway == "" {
			return fmt.Errorf("environment %q does not exist and no gateway was given to create it", env)
		}
		if err := cli.AddEnvironmentOn(env, gateway, "orama setup on "+network, network); err != nil {
			return err
		}
	case gateway != "":
		if err := cli.AddEnvironmentOn(env, gateway, existing.Description, network); err != nil {
			return err
		}
	}
	for _, n := range nodes {
		if err := cli.UpsertEnvNode(env, cli.EnvNode{Host: n.Host, User: n.User, Role: n.Role}); err != nil {
			return err
		}
	}
	return nil
}

func (cliRecorder) Operator(env, operator string) error { return cli.RecordOperator(env, operator) }

func (cliRecorder) Hosts(env string) []RecordedNode {
	e, err := cli.GetEnvironmentByName(env)
	if err != nil || e == nil {
		return nil
	}
	nodes := make([]RecordedNode, len(e.Nodes))
	for i, n := range e.Nodes {
		nodes[i] = RecordedNode{Host: n.Host, User: n.User, Role: n.Role}
	}
	return nodes
}

// ActiveFor is the active environment when it runs on network.
func (cliRecorder) ActiveFor(network string) string {
	e, err := cli.GetActiveEnvironment()
	if err != nil || e == nil || e.Network != network {
		return ""
	}
	return e.Name
}
