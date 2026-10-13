package rqlite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// rqlited's raft recovery (RecoverNode, run when it finds raft/peers.json)
// rebuilds the database from the latest snapshot into <dataDir>/recovery.db and
// then removes recovery.db only. The -wal and -shm files SQLite opened beside
// it stay behind (rqlite v10.4.0), as do the restore-wal-N.tmp files the
// snapshot restore extracted when it failed part way.
//
// A leftover recovery.db-wal makes the next recovery fail for good: restoring a
// snapshot that carries WAL files replays them into recovery.db, and the replay
// refuses to start when a WAL already sits beside the database
// ("cannot replay WAL files: existing WAL file present"). The failure is
// deterministic and rqlited exits before opening its store, so the unit
// crash-loops with the recovery peers.json still waiting.
const (
	recoveryDBFile        = "recovery.db"
	restoreWALPattern     = "restore-wal-*.tmp"
	recoveryLeftoverGlobs = recoveryDBFile + "*"
)

// RemoveRecoveryLeftovers deletes what an earlier rqlited raft recovery left in
// dataDir, so the recovery a peers.json is about to trigger starts clean. It
// returns the names it removed.
//
// Call it only while rqlited is not running on dataDir, immediately before
// writing a recovery peers.json: recovery.db is rqlited's scratch database
// during a recovery, and nothing else reads it.
func RemoveRecoveryLeftovers(dataDir string) ([]string, error) {
	var removed []string
	for _, pattern := range []string{recoveryLeftoverGlobs, restoreWALPattern} {
		matches, err := filepath.Glob(filepath.Join(dataDir, pattern))
		if err != nil {
			return removed, fmt.Errorf("list %s in %s: %w", pattern, dataDir, err)
		}
		for _, path := range matches {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, fmt.Errorf("remove the leftover %s of an earlier rqlite recovery (rqlite cannot recover while it exists): %w", path, err)
			}
			removed = append(removed, filepath.Base(path))
		}
	}
	return removed, nil
}
