package node

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/legacylayout"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/unitenv"
)

// legacyLayout is the migration with everything but the orama directory and
// the log set: the host's unit env tree and orama-privhelper. A variable only so
// a test can migrate without either.
var legacyLayout = legacylayout.Migrator{UnitEnvDir: unitenv.Dir, Stager: legacylayout.PrivHelperStager{}}

// migrateLegacyLayout moves what the pre-0.200 layout holds — the index
// gateway's signing keys, tenant SQLite, deployments, the shared TURN config,
// the namespace units' env files and the deployments' env files and tokens —
// into the current one (pkg/legacylayout).
//
// It runs here, as the orama user, because orama-node owns the orama
// directory (ReadWritePaths) and is the first thing to start after an
// upgrade: root never has to walk a tree the orama user can plant symlinks in.
// A refusal (a path on both layouts) holds every other component, so the node
// stays down and says why instead of starting on half of each layout.
func (n *Node) migrateLegacyLayout(context.Context) error {
	oramaDir, err := n.oramaDir()
	if err != nil {
		return err
	}
	m := legacyLayout
	m.OramaDir = oramaDir
	m.Logf = func(format string, args ...any) {
		n.logger.ComponentInfo(logging.ComponentNode, "Legacy layout: "+fmt.Sprintf(format, args...))
	}
	if err := m.Run(); err != nil {
		return fmt.Errorf("move this node's state off the pre-0.200 layout under %s: %w", oramaDir, err)
	}
	return nil
}
