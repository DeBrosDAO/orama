package install

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

// MigrateIndexerLayout runs the installed orama-global to move the chain indexer's index aside
// when it was written in an older layout than that binary's, so the indexer's next start begins a
// new one instead of refusing the old (chain/indexer, MoveAsideStale). An index of the current
// layout, or none, is left alone. The old index is renamed under the indexer's state directory
// with its layout version and the time, never deleted. It returns what the command said.
//
// The indexer must be stopped: the command opens the index read-only and fails while the indexer
// holds it. It runs as the indexer's own account (runuser), after the release's orama-global is in
// place, not as root: the account owns the directory and everything in it, so a root process that
// opened and renamed what is there would act on whatever the account had put at that path, a link
// to another directory included. As the account it can touch only what the account already can, and
// what it renames stays owned by the account.
func MigrateIndexerLayout(h GlobalHost) (string, error) {
	bin := filepath.Join(h.BinDir, globalServiceBin)
	out, err := h.Run("runuser", "-u", globalIndexerUser, "--", bin, "indexer", "migrate", "--home", constants.GlobalIndexerHome)
	said := strings.TrimSpace(string(out))
	if err != nil {
		return said, fmt.Errorf("failed to move the chain indexer's old index aside (runuser -u %s -- %s indexer migrate --home %s): %w\n%s",
			globalIndexerUser, bin, constants.GlobalIndexerHome, err, said)
	}
	return said, nil
}
