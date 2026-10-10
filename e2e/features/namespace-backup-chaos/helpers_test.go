//go:build e2e_fleet

package namespacebackupchaos

import (
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

const (
	pollEvery = 2 * time.Second
	// rotationClusterName is the eval cluster the rotation test installs.
	rotationClusterName = "rotate"
	// openCreation lets any signed-in wallet create a namespace
	// (docs/whitepaper/technical-reference/appendices/d-cli-reference.md "orama maint cluster settings set").
	openCreation = "open"
)

// rotationCluster installs a single-node eval cluster for the test (about
// fifteen minutes; harness.ExtraCluster removes it at the end) and returns
// it as a fleet the namespace helpers accept, with a CLI whose current
// environment is that cluster. `orama maint operator rotate-secrets --rotate`
// replaces the cluster's encryption root for good, so it must never run on
// the run's own fleet, whose later stages would test a rotated cluster.
func rotationCluster(t testing.TB) (*fleet.Fleet, *oramacli.Runner) {
	t.Helper()
	f := harness.Fleet(t)
	cl := harness.ExtraCluster(t, rotationClusterName)
	cli := harness.CLI(t).Isolated(t)
	cli.MustOK(t, "network", "use", cl.Env)
	// The cluster is this test's alone and removed afterwards: nothing to restore.
	cli.MustOK(t, "maint", "cluster", "settings", "set", "namespace-creation", openCreation)
	st := &fleet.State{RunID: f.State.RunID, Env: cl.Env, BaseDomain: cl.BaseDomain, GatewayURL: cl.GatewayURL,
		CAFile: cl.CAFile, Nodes: []fleet.Node{cl.Node}, OramaBin: f.State.OramaBin, Home: cli.Home,
		RWSock: f.State.RWSock, OperatorAddress: f.State.OperatorAddress, SSHKeyFile: f.State.SSHKeyFile,
		KnownHostsFile: f.State.KnownHostsFile, ArtifactDir: f.State.ArtifactDir}
	return fleet.New(st, f.Recorder()), cli
}
