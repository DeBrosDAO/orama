package relupgrade

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/releasefetch"
	"github.com/DeBrosOfficial/network/pkg/rollout"
)

// archByMachine maps `uname -m` to the architectures a release is built for.
var archByMachine = map[string]string{
	"x86_64": "amd64", "amd64": "amd64",
	"aarch64": "arm64", "arm64": "arm64",
}

// releaseSet is the verified releases fetched for one upgrade, by architecture.
type releaseSet struct {
	byArch map[string]*releasefetch.Release
	// archOf is each planned node's architecture, read once.
	archOf  map[string]string
	version string
}

// remove deletes what the fetches left on this machine.
func (s *releaseSet) remove() {
	for _, rel := range s.byArch {
		_ = rel.Remove()
	}
}

// fetchReleases fetches the channel's newest release for each architecture the
// planned nodes run, and requires them to be one version.
func (r *runner) fetchReleases(ctx context.Context, steps []rollout.Step, out io.Writer) (*releaseSet, error) {
	set := &releaseSet{byArch: map[string]*releasefetch.Release{}, archOf: map[string]string{}}
	for _, step := range steps {
		machine, err := r.seams.arch(step.Node)
		if err != nil {
			return nil, set.fail(err)
		}
		arch, ok := archByMachine[strings.TrimSpace(machine)]
		if !ok {
			return nil, set.fail(clierr.Failure("%s reports the machine type %q; releases are built for amd64 and arm64", step.Node.Host, strings.TrimSpace(machine)))
		}
		set.archOf[step.Node.Host] = arch
		if _, done := set.byArch[arch]; done {
			continue
		}
		fmt.Fprintf(out, "Fetching the newest %s release of %s for linux/%s...\n", r.target.Manifest.Channel, r.target.Network, arch)
		rel, err := r.seams.fetch(ctx, r.target.FetchParams(r.home, arch, r.now))
		if err != nil {
			return nil, set.fail(clierr.Failure("%v", err))
		}
		set.byArch[arch] = rel
		if set.version != "" && set.version != rel.Version {
			return nil, set.fail(clierr.Conflict("the channel's newest release is %s for one architecture and %s for another; "+
				"the release is still being published, run `orama upgrade` again once it is complete", set.version, rel.Version))
		}
		set.version = rel.Version
	}
	if len(set.byArch) == 0 {
		return nil, clierr.Failure("no node to upgrade")
	}
	return set, nil
}

// fail removes the fetches already made and returns err.
func (s *releaseSet) fail(err error) error {
	s.remove()
	return err
}
