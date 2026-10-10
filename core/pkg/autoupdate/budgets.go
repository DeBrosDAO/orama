package autoupdate

import (
	"time"

	"github.com/DeBrosOfficial/network/pkg/nodehealth"
)

// The time an install may take, fitted inside the two clocks that end it:
// the rollout lock's lease (LockTTL), after which another node may take the
// lock, and the service's TimeoutStartSec (RunTimeout), after which systemd
// kills the run. Nothing renews the lease, so the work under the lock has to
// fit in it.
const (
	// fetchBudget bounds fetching the metadata, and again the archive.
	fetchBudget = 10 * time.Minute
	// UpgradeBudget bounds one `orama node upgrade --restart`.
	UpgradeBudget = 12 * time.Minute
	// healthBudget bounds one wait for the node to rejoin the cluster.
	healthBudget = nodehealth.DefaultBudget
	// installBudget is the longest the work under the lock takes: the archive,
	// then an upgrade and its gate, then, when that fails, an upgrade onto the
	// previous release and its gate. Staging and restoring are renames.
	installBudget = fetchBudget + 2*(UpgradeBudget+healthBudget)
	// RunTimeout is the service's TimeoutStartSec: the metadata fetch, then an
	// install.
	RunTimeout = time.Hour
)
