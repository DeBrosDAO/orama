package removenode

import (
	"context"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/monitor"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// nodeIsValidator reads from the cluster's telemetry, the snapshot `orama
// status` shows, whether the node's chain key is in the validator set. known is
// false when the node sent no report, its report has no chain section, or the
// chain did not answer the collector: the telemetry then cannot say. A validator
// that is jailed has no voting power and is not in the set either, which this
// cannot see; 'orama chain validator' lists it.
func nodeIsValidator(ctx context.Context, env, host string) (isValidator, known bool, err error) {
	src, err := monitor.NewSource(monitor.Options{Env: env, SSHTimeout: monitor.DefaultSSHTimeout})
	if err != nil {
		return false, false, err
	}
	snap, err := src.Snapshot(ctx)
	if err != nil {
		return false, false, err
	}
	isValidator, known = validatorStatus(snap, host)
	return isValidator, known, nil
}

// validatorStatus reads the node's chain section of the snapshot. A section that
// carries an error, or whose RPC did not answer, has not looked at the validator
// set: its IsValidator is false because nothing was read, which is when a crashed
// validator is most likely to be removed.
func validatorStatus(snap *cluster.ClusterSnapshot, host string) (isValidator, known bool) {
	rep := snap.ByHost()[host]
	if rep == nil || rep.Chain == nil || rep.Chain.Error != "" || !rep.Chain.Responsive {
		return false, false
	}
	return rep.Chain.IsValidator, true
}
