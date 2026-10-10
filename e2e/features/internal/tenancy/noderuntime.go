//go:build e2e_fleet

package tenancy

import (
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// The interpreters a Node.js deployment's units execute
// (core/systemd/orama-deploy-node@.service, orama-deploy-npm@.service,
// orama-deploy-build@.service ExecStart).
const (
	NodeBinary = "/usr/bin/node"
	NPMBinary  = "/usr/bin/npm"
)

// RequireNodeRuntime fails, never skips, when a core node lacks one of bins
// (NodeBinary by default): the platform documents Node.js and Next.js SSR
// deployments, but install only puts curl, wget, unzip and sudo on a node
// (core/pkg/install/prebuilt.go installMinimalDeps), so their units cannot
// start. The failure names the gap instead of surfacing as "failed to start
// service" deep inside a deploy.
func RequireNodeRuntime(t testing.TB, f *fleet.Fleet, bins ...string) {
	t.Helper()
	if len(bins) == 0 {
		bins = []string{NodeBinary}
	}
	for _, n := range f.State.Nodes {
		for _, bin := range bins {
			if out := f.Exec(t, n, "test -x "+bin); out.Exit != 0 {
				t.Fatalf("node runtime missing on nodes: %s has no %s, so a Node.js deployment's unit cannot start there", n.Name, bin)
			}
		}
	}
}
