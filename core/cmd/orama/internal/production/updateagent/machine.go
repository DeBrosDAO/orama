package updateagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/production/push"
	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/install"
	"github.com/DeBrosOfficial/network/pkg/nodehealth"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// installedCLI is the orama the staged release carries. The upgrade runs
// through it, not through this process: `orama node upgrade` re-executes the
// binary it installs, and the agent is not that command.
var installedCLI = filepath.Join(install.OramaBase, "bin", "orama")

// machine is this node as autoupdate.Node.
type machine struct {
	current string
	health  nodehealth.Target
}

var _ autoupdate.Node = (*machine)(nil)

// newMachine reads the release this node runs from its installed manifest.
func newMachine(ep rqlite.Endpoint) (*machine, error) {
	data, err := os.ReadFile(filepath.Join(install.OramaBase, archivetrust.ManifestName))
	if err != nil {
		return nil, fmt.Errorf("read the installed release's manifest: %w", err)
	}
	manifest, err := archivetrust.ParseManifest(data)
	if err != nil {
		return nil, err
	}
	return &machine{
		current: manifest.Version,
		health: nodehealth.Target{
			RQLite:      ep,
			GatewayBase: fmt.Sprintf("http://localhost:%d", constants.GatewayAPIPort),
		},
	}, nil
}

func (m *machine) Current() string { return m.current }

// Stage puts the downloaded release in place under /opt/orama on the release
// root's checks alone, keeping the release it replaces.
func (m *machine) Stage(_ context.Context, rel autoupdate.Release) error {
	return push.Stage(push.StageOptions{
		Archive:         rel.ArchivePath(),
		ReleaseMetadata: rel.MetadataDir(),
		ReleaseTarget:   rel.Target.Path,
		ReleaseOnly:     true,
		KeepPrevious:    true,
	})
}

// Upgrade runs `orama node upgrade --restart` of the release in place. An exit
// with the preflight code means a check refused before any service stopped, and
// a CLI that could not be started changed nothing either.
func (m *machine) Upgrade(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, installedCLI, "node", "upgrade", "--restart")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err != nil && cmd.Process == nil:
		return fmt.Errorf("start %s: %w", installedCLI, errors.Join(autoupdate.ErrNotStarted, err))
	case errors.As(err, &exit) && exit.ExitCode() == clierr.CodePreflight:
		return fmt.Errorf("orama node upgrade refused before stopping anything: %w", errors.Join(autoupdate.ErrNotStarted, err))
	}
	if err != nil {
		return fmt.Errorf("orama node upgrade --restart: %w", err)
	}
	return nil
}

func (m *machine) Restore(context.Context) error { return push.RestorePrevious() }

// Healthy waits for the node to carry its share of the cluster again.
func (m *machine) Healthy(ctx context.Context) error {
	return nodehealth.WaitReady(ctx, m.health, nodehealth.Options{RequireLeaderKnown: true})
}
