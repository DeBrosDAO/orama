package provision

import (
	"context"
	"fmt"
	"os"

	"github.com/DeBrosOfficial/network/e2e/harness/broker"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// A feature process has no cloud credentials (stages.FeatureEnv): it has the
// runner's broker instead, and AddExtra, RemoveExtra, DestroyNode,
// AddEvalCluster and RemoveEvalCluster send the operation there. Whether a
// process is one is decided by E2E_BROKER_SOCK alone, never by a missing
// token, so a misconfigured runner fails instead of quietly switching path.

// runBroker is the run's broker when this is a feature process, else nil.
func runBroker() (*broker.Client, error) {
	c, err := broker.FromEnv(os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("the run's broker: %w", err)
	}
	return c, nil
}

// brokerDestroy destroys the extra at host through the broker and drops it
// from st. Core nodes and probes are not the broker's to destroy.
func brokerDestroy(ctx context.Context, b *broker.Client, st *fleet.State, host string) error {
	for i, n := range st.Extras {
		if n.PublicIP == host {
			if err := b.RemoveExtra(ctx, n.Name); err != nil {
				return err
			}
			st.Extras = append(st.Extras[:i], st.Extras[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("%s is not an extra server of run %s: a feature process destroys only extras it created; "+
		"core nodes and probes need the cloud credentials, which only the runner holds (`e2e-fleet hook destroy` run outside any feature process)",
		host, st.RunID)
}

// brokerRemoveExtra removes the extra called name through the broker and
// drops it from st.
func brokerRemoveExtra(ctx context.Context, b *broker.Client, st *fleet.State, name string) error {
	if err := b.RemoveExtra(ctx, name); err != nil {
		return err
	}
	for i, n := range st.Extras {
		if n.Name == name {
			st.Extras = append(st.Extras[:i], st.Extras[i+1:]...)
			break
		}
	}
	return nil
}

// Direct is provisioning with this process's own credentials, bypassing any
// broker: what the broker itself runs (broker.Cloud).
type Direct struct{}

// AddExtra is AddExtra with the environment's credentials.
func (Direct) AddExtra(ctx context.Context, st *fleet.State, name, location string) (fleet.Node, error) {
	d, err := depsFromEnv()
	if err != nil {
		return fleet.Node{}, err
	}
	limit, ttl, err := limitsFromEnv()
	if err != nil {
		return fleet.Node{}, err
	}
	if location == "" {
		location = runLocation(st)
	}
	return addExtra(ctx, st, name, location, extraLimits{envOr(EnvServerType, DefaultServerType), limit, ttl}, d)
}

// RemoveExtra is RemoveExtra with the environment's credentials.
func (Direct) RemoveExtra(ctx context.Context, st *fleet.State, name string) error {
	d, err := depsFromEnv()
	if err != nil {
		return err
	}
	return removeExtra(ctx, st, name, d)
}

// AddCluster is AddEvalCluster with the environment's credentials.
func (Direct) AddCluster(ctx context.Context, st *fleet.State, name string) (fleet.Cluster, error) {
	d, err := depsFromEnv()
	if err != nil {
		return fleet.Cluster{}, err
	}
	limit, ttl, err := limitsFromEnv()
	if err != nil {
		return fleet.Cluster{}, err
	}
	lim := extraLimits{envOr(EnvServerType, DefaultServerType), limit, ttl}
	return addEvalCluster(ctx, st, name, lim, stderrLogger{}, d)
}

// RemoveCluster is RemoveEvalCluster with the environment's credentials.
func (Direct) RemoveCluster(ctx context.Context, st *fleet.State, name string) error {
	d, err := depsFromEnv()
	if err != nil {
		return err
	}
	return removeEvalCluster(ctx, st, name, stderrLogger{}, d)
}

// runLocation is where the run's core nodes are.
func runLocation(st *fleet.State) string {
	if len(st.Nodes) > 0 && st.Nodes[0].Location != "" {
		return st.Nodes[0].Location
	}
	return DefaultLocation
}

// stderrLogger is the progress of a brokered operation on the runner's stderr.
type stderrLogger struct{}

func (stderrLogger) Infof(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "e2e-fleet broker: "+format+"\n", args...)
}
