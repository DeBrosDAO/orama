package monitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// Mode names where a Source reads the cluster from.
type Mode string

const (
	// ModeAPI reads the gateway's operator telemetry API. The default.
	ModeAPI Mode = "api"
	// ModeSSH runs `orama node report` on every node over SSH: the break-glass
	// path for when no gateway answers. Only chosen by --ssh.
	ModeSSH Mode = "ssh"
)

// Refresh intervals for the live view.
const (
	// DefaultInterval is how often the live view asks for a snapshot.
	DefaultInterval = 5 * time.Second
	// MinAPIInterval is the shortest stream interval the gateway serves.
	MinAPIInterval = 2 * time.Second
	// MaxAPIInterval is the longest stream interval the gateway serves.
	MaxAPIInterval = time.Minute
	// MinSSHInterval bounds --ssh refreshes: each one SSHes into every node
	// and runs a full report there.
	MinSSHInterval = 15 * time.Second
	// DefaultSSHTimeout bounds one node's report over SSH.
	DefaultSSHTimeout = 30 * time.Second
)

// Options says where to read the cluster from and which part of it to show.
type Options struct {
	Env string
	// Node limits the view to one node, by public host or WireGuard IP.
	Node string
	// ConfigPath is a nodes.conf to read instead of resolving nodes. SSH only.
	ConfigPath string
	// SSH selects the SSH source. Nothing else does.
	SSH bool
	// SSHTimeout bounds each node's report in SSH mode.
	SSHTimeout time.Duration
}

// Source produces cluster snapshots.
type Source interface {
	Mode() Mode
	// Snapshot reads the cluster once.
	Snapshot(ctx context.Context) (*cluster.ClusterSnapshot, error)
	// Watch delivers snapshots and connection changes about every interval
	// until ctx ends or the source cannot continue. The channel is closed then.
	Watch(ctx context.Context, interval time.Duration) <-chan Update
}

// LinkState is the state of a live view's connection to its source.
type LinkState int

const (
	LinkConnecting LinkState = iota
	LinkLive
	LinkReconnecting
	// LinkFailed means the source gave up: retrying cannot help (for example
	// the credential was refused). The Update's Err says why.
	LinkFailed
)

// Update is one event from Watch. Snapshot is set when new data arrived; Err
// says what went wrong while State is LinkReconnecting or LinkFailed, or
// carries an error the gateway reported without dropping the stream.
type Update struct {
	State    LinkState
	Snapshot *cluster.ClusterSnapshot
	Err      error
	RetryIn  time.Duration
	Attempt  int
}

// NewSource builds the source opts ask for. The API is the default; SSH is
// used only when opts.SSH is set, never as a fallback, so a gateway outage is
// reported as one rather than hidden behind a slower path.
func NewSource(opts Options) (Source, error) {
	if opts.ConfigPath != "" && !opts.SSH {
		return nil, clierr.Usage("--config names a nodes file for SSH collection; it only applies with --ssh")
	}
	var inner Source
	if opts.SSH {
		inner = &sshSource{cfg: CollectorConfig{
			ConfigPath: opts.ConfigPath,
			Env:        opts.Env,
			Timeout:    opts.SSHTimeout,
		}}
	} else {
		api, err := newAPISourceForEnv(opts.Env)
		if err != nil {
			return nil, err
		}
		inner = api
	}
	return &scopedSource{inner: inner, env: opts.Env, node: opts.Node}, nil
}

// ResolveInterval validates the live view's refresh interval. explicit is
// whether the operator passed --interval; an unset interval in SSH mode is
// raised to the SSH minimum rather than refused.
func ResolveInterval(requested time.Duration, explicit, ssh bool) (time.Duration, error) {
	if ssh {
		if requested >= MinSSHInterval {
			return requested, nil
		}
		if explicit {
			return 0, clierr.Usage("--interval must be at least %s with --ssh: every refresh SSHes into every node", MinSSHInterval)
		}
		return MinSSHInterval, nil
	}
	if requested < MinAPIInterval || requested > MaxAPIInterval {
		return 0, clierr.Usage("--interval must be between %s and %s", MinAPIInterval, MaxAPIInterval)
	}
	return requested, nil
}

// scopedSource cleans every snapshot and every error of terminal control
// characters, names the environment, and narrows the snapshot to one node when
// asked.
type scopedSource struct {
	inner Source
	env   string
	node  string
}

func (s *scopedSource) Mode() Mode { return s.inner.Mode() }

func (s *scopedSource) Snapshot(ctx context.Context) (*cluster.ClusterSnapshot, error) {
	snap, err := s.inner.Snapshot(ctx)
	if err != nil {
		return nil, cleanErr(err)
	}
	snap, err = s.scope(snap)
	return snap, cleanErr(err)
}

func (s *scopedSource) Watch(ctx context.Context, interval time.Duration) <-chan Update {
	in := s.inner.Watch(ctx, interval)
	out := make(chan Update)
	go func() {
		defer close(out)
		for u := range in {
			if u.Snapshot != nil {
				snap, err := s.scope(u.Snapshot)
				u.Snapshot, u.Err = snap, err
			}
			u.Err = cleanErr(u.Err)
			select {
			case out <- u:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func (s *scopedSource) scope(snap *cluster.ClusterSnapshot) (*cluster.ClusterSnapshot, error) {
	sanitizeSnapshot(snap)
	snap.Environment = s.env
	if s.node == "" {
		return snap, nil
	}
	return FilterNode(snap, s.node)
}

// FilterNode narrows a snapshot to the node whose public host or WireGuard IP
// is host, with the alerts raised about it. It is an error when no node
// matches, naming the ones that are there.
func FilterNode(snap *cluster.ClusterSnapshot, host string) (*cluster.ClusterSnapshot, error) {
	out := *snap
	out.Nodes = nil
	out.Alerts = nil
	var known []string
	for _, n := range snap.Nodes {
		known = append(known, n.Node.Host)
		if n.Node.Host == host || (n.Node.WGIP != "" && n.Node.WGIP == host) {
			out.Nodes = append(out.Nodes, n)
		}
	}
	if len(out.Nodes) == 0 {
		return nil, clierr.NotFound("node %s is not in the %s snapshot (nodes: %s)",
			host, snap.Environment, strings.Join(known, ", "))
	}
	names := map[string]bool{host: true, out.Nodes[0].Node.Host: true}
	for _, a := range snap.Alerts {
		if names[a.Node] {
			out.Alerts = append(out.Alerts, a)
		}
	}
	return &out, nil
}

// sshUnavailableHint is appended to every API failure: the one thing an
// operator can do when the gateways themselves are the problem.
const sshUnavailableHint = "if the gateways are down, read the nodes directly with --ssh"

func withSSHHint(err error) error {
	return fmt.Errorf("%w (%s)", err, sshUnavailableHint)
}
