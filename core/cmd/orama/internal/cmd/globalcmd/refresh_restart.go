package globalcmd

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/globalnode"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// restartRefreshed restarts the services that run a replaced binary, and sees to the chain
// indexer's index while the indexer is stopped.
//
// An index written in an older layout than the new orama-global's is refused by the indexer, which
// would crash-loop on every start. So the indexer is stopped, `orama-global indexer migrate` moves
// an old index aside (the indexer then starts a new one, rebuilt from the chain), and the indexer is
// started. That covers three states of the unit: running the replaced binary (restart), already
// crash-looping on an old index because an earlier upgrade did not move it (failed or activating),
// and stopped on purpose (the index is put right and the unit stays stopped). A running indexer
// that does not run a replaced binary opened its index, so the index is current and is not looked at.
func restartRefreshed(ctx context.Context, out io.Writer, life globalnode.Lifecycle, host install.GlobalHost, installed, restart []install.GlobalService) error {
	stop, start := slices.Clone(restart), slices.Clone(restart)
	migrate := slices.Contains(restart, install.GlobalServiceIndexer)
	if slices.Contains(installed, install.GlobalServiceIndexer) && !migrate {
		state, err := indexerState(life)
		if err != nil {
			return clierr.Failure("%v", err)
		}
		switch state {
		case "active":
		case "inactive":
			migrate = true
		default:
			migrate = true
			stop = append(stop, install.GlobalServiceIndexer)
			start = append(start, install.GlobalServiceIndexer)
		}
	}
	if len(stop) == 0 && !migrate {
		fmt.Fprintln(out, "  global binaries: no running service needs a restart")
		return nil
	}
	// An empty list would mean every installed service to the lifecycle.
	if len(stop) > 0 {
		if err := life.Stop(stop); err != nil {
			return clierr.Failure("stop the services that run a replaced binary: %v", err)
		}
	}
	if migrate {
		said, err := install.MigrateIndexerLayout(host)
		if err != nil {
			return clierr.Failure("%v\nthe services %s are stopped; once the index is dealt with, start them with 'orama global start %s'", err, serviceNames(start), serviceNames(start))
		}
		fmt.Fprintln(out, said)
	}
	if len(start) == 0 {
		return nil
	}
	if err := life.Start(ctx, start); err != nil {
		return clierr.Failure("restart the services that run a replaced binary: %v", err)
	}
	return nil
}

// indexerState is the indexer unit's systemd state: "active", "inactive" (stopped), or another
// ("failed", "activating") for a unit that is not staying up.
func indexerState(life globalnode.Lifecycle) (string, error) {
	states, err := life.Status()
	if err != nil {
		return "", err
	}
	for _, s := range states {
		if s.Service == install.GlobalServiceIndexer {
			return s.Active, nil
		}
	}
	return "", fmt.Errorf("the chain indexer is not installed")
}

func serviceNames(services []install.GlobalService) string {
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = string(s)
	}
	return strings.Join(names, " ")
}
