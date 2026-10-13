package relupgrade

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/pkg/inspector"
	"github.com/DeBrosOfficial/network/pkg/remotessh"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// nodeArchCommand prints a node's machine type.
const nodeArchCommand = "uname -m"

// nodeArch reads the machine type of a node over SSH.
func nodeArch(node inspector.Node) (string, error) {
	out, err := remotessh.RunSSHOutput(node, nodeArchCommand)
	if err != nil {
		return "", fmt.Errorf("read the machine type of %s: %w", node.Host, err)
	}
	return strings.TrimSpace(out), nil
}

// snapshotStates reads what each node runs from the cluster's telemetry, the
// same snapshot `orama status` shows: through the gateway, or over SSH with ssh.
func snapshotStates(ctx context.Context, env string, ssh bool) (map[string]NodeState, error) {
	src, err := monitor.NewSource(monitor.Options{Env: env, SSH: ssh, SSHTimeout: monitor.DefaultSSHTimeout})
	if err != nil {
		return nil, err
	}
	snap, err := src.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return statesFromSnapshot(snap), nil
}

// statesFromSnapshot is the version and global layer of every node that
// reported. A node that did not report is absent: its version is unknown.
func statesFromSnapshot(snap *cluster.ClusterSnapshot) map[string]NodeState {
	states := map[string]NodeState{}
	for host, rep := range snap.ByHost() {
		states[host] = NodeState{
			Version: strings.TrimPrefix(rep.Version, "v"),
			Global:  rep.Global != nil && len(rep.Global.Units) > 0,
		}
	}
	return states
}
