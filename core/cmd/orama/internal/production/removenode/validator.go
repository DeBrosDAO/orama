package removenode

import (
	"context"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
)

// nodeIsValidator reads from the cluster's telemetry, the snapshot `orama
// status` shows, whether the node's chain key is in the validator set. known is
// false when the node sent no report, or its report has no chain section: the
// telemetry then cannot say.
func nodeIsValidator(ctx context.Context, env, host string) (isValidator, known bool, err error) {
	src, err := monitor.NewSource(monitor.Options{Env: env, SSHTimeout: monitor.DefaultSSHTimeout})
	if err != nil {
		return false, false, err
	}
	snap, err := src.Snapshot(ctx)
	if err != nil {
		return false, false, err
	}
	rep := snap.ByHost()[host]
	if rep == nil || rep.Chain == nil {
		return false, false, nil
	}
	return rep.Chain.IsValidator, true, nil
}
