package removenode

import (
	"context"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
)

// nodeIsValidator reads from the cluster's telemetry, the snapshot `orama
// status` shows, whether the node's chain key is in the validator set. A node
// that sent no report, or runs no chain, is not a validator as far as anyone
// can tell.
func nodeIsValidator(ctx context.Context, env, host string) (bool, error) {
	src, err := monitor.NewSource(monitor.Options{Env: env, SSHTimeout: monitor.DefaultSSHTimeout})
	if err != nil {
		return false, err
	}
	snap, err := src.Snapshot(ctx)
	if err != nil {
		return false, err
	}
	rep := snap.ByHost()[host]
	return rep != nil && rep.Chain != nil && rep.Chain.IsValidator, nil
}
