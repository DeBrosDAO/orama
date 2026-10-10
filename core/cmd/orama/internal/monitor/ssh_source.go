package monitor

import (
	"context"
	"time"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// sshSource collects every node's report over SSH (CollectOnce). It does not
// depend on any gateway, which is why it exists: the break-glass view when the
// API cannot answer.
type sshSource struct {
	cfg CollectorConfig
}

func (s *sshSource) Mode() Mode { return ModeSSH }

func (s *sshSource) Snapshot(ctx context.Context) (*cluster.ClusterSnapshot, error) {
	return CollectOnce(ctx, s.cfg)
}

// Watch collects, then waits interval, then collects again. A collection that
// fails as a whole (the node list cannot be resolved, say) is reported and
// retried on the next tick, unless retrying cannot fix it (a --config file
// that cannot be read, an environment with no nodes): then the watch stops. A
// node that fails is part of the snapshot.
func (s *sshSource) Watch(ctx context.Context, interval time.Duration) <-chan Update {
	out := make(chan Update)
	go func() {
		defer close(out)
		for attempt := 1; ; attempt++ {
			u := Update{State: LinkLive}
			snap, err := CollectOnce(ctx, s.cfg)
			if err != nil && isFatal(err, false) {
				send(ctx, out, Update{State: LinkFailed, Err: err})
				return
			}
			if err != nil {
				u = Update{State: LinkReconnecting, Err: err, RetryIn: interval, Attempt: attempt}
			} else {
				u.Snapshot = snap
				attempt = 0
			}
			if !send(ctx, out, u) || sleepCtx(ctx, interval) != nil {
				return
			}
		}
	}()
	return out
}

// send delivers u unless ctx ends first.
func send(ctx context.Context, out chan<- Update, u Update) bool {
	select {
	case out <- u:
		return true
	case <-ctx.Done():
		return false
	}
}

// sleepCtx waits d, or returns ctx's error if it ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
