//go:build e2e_fleet

package realistic

import (
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// reachSeconds bounds the reachability probe of an outside URL from a node.
const reachSeconds = 20

// RequireReachableFromNode makes t not applicable when node n cannot fetch
// url directly: a test whose product path leaves the fleet for a public
// site (example.com, check.torproject.org) measures that site's reachability
// from the node, not the product, when the site is down or filtered there.
// The probe runs on the node (where the product dials from), never on the
// runner, and the skip says so.
func RequireReachableFromNode(t testing.TB, f *fleet.Fleet, n fleet.Node, url string) {
	t.Helper()
	out := f.Exec(t, n, fmt.Sprintf("curl -sS -o /dev/null --max-time %d %s", reachSeconds, fleet.ShellQuote(url)))
	if out.Exit != 0 {
		harness.SkipNotApplicable(t, fmt.Sprintf("%s is not reachable from node %s (curl exit %d: %s); the check depends on that outside site, probed from the node, not the runner",
			url, n.Name, out.Exit, f.Redact(out.Stderr)))
	}
}
