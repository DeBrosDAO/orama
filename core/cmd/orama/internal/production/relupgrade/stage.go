package relupgrade

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/DeBrosOfficial/network/pkg/rollout"
	"golang.org/x/sync/errgroup"
)

// stageParallel bounds how many nodes are staged at once: staging uploads the
// archive from this machine, so more would only share its uplink.
const stageParallel = 4

// stageAll stages the verified release on every node the plan restarts, before
// any of them is restarted. A node that refuses leaves the rest staged and
// nothing restarted: staging replaces files, never services.
func (r *runner) stageAll(steps []rollout.Step, releases *releaseSet, out io.Writer) error {
	fmt.Fprintf(out, "\nStaging %s on %d node(s)...\n", releases.version, len(steps))
	var (
		mu   sync.Mutex
		errs []error
		g    errgroup.Group
	)
	g.SetLimit(stageParallel)
	for _, step := range steps {
		g.Go(func() error {
			err := r.stageNode(step, releases)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				fmt.Fprintf(out, "  ✗ %s: %v\n", step.Node.Host, err)
				return nil
			}
			fmt.Fprintf(out, "  ✓ %s staged\n", step.Node.Host)
			return nil
		})
	}
	_ = g.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("%d node(s) refused the release, so nothing was restarted: %w", len(errs), errors.Join(errs...))
	}
	return nil
}

// stageNode stages the release for the node's architecture on one node.
func (r *runner) stageNode(step rollout.Step, releases *releaseSet) error {
	machine, err := r.seams.arch(step.Node)
	if err != nil {
		return err
	}
	rel := releases.byArch[archByMachine[strings.TrimSpace(machine)]]
	if rel == nil {
		return fmt.Errorf("no release was fetched for machine type %q", strings.TrimSpace(machine))
	}
	_, err = r.seams.stage(step.Node, push.ReleaseFiles{
		Archive: rel.ArchivePath, MetadataDir: rel.MetadataDir, Target: rel.Target, Root: rel.Root,
	})
	return err
}
