package updateagent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/autoupdate"
	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/install"
)

// nodeRole is autoupdate.RoleValidator on a machine that runs the chain and
// RoleCluster on any other. A validator is never updated automatically: its
// chain binary changes by hand (`orama global stage-oramad`), in step with the
// other validators; on mode auto its agent says so and records the release as
// skipped (autoupdate.ActionSkip).
func nodeRole() (string, error) {
	return roleIn(constants.SystemdUnitDir)
}

func roleIn(unitDir string) (string, error) {
	chain := filepath.Join(unitDir, install.GlobalServiceUnit(install.GlobalServiceOrder[0]))
	_, err := os.Lstat(chain)
	switch {
	case err == nil:
		return autoupdate.RoleValidator, nil
	case errors.Is(err, fs.ErrNotExist):
		return autoupdate.RoleCluster, nil
	default:
		return "", fmt.Errorf("check for the chain unit %s: %w", chain, err)
	}
}
