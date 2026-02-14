package rqlite

import (
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/config"
)

func (r *RQLiteManager) rqliteDataDirPath() (string, error) {
	dataDir, err := config.ExpandPath(r.dataDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "rqlite"), nil
}

func (r *RQLiteManager) resolveMigrationsDir() (string, error) {
	productionPath := "/home/orama/src/migrations"
	if _, err := os.Stat(productionPath); err == nil {
		return productionPath, nil
	}
	return "migrations", nil
}

func (r *RQLiteManager) prepareDataDir() (string, error) {
	rqliteDataDir, err := r.rqliteDataDirPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(rqliteDataDir, 0755); err != nil {
		return "", err
	}
	return rqliteDataDir, nil
}

func (r *RQLiteManager) hasExistingState(rqliteDataDir string) bool {
	// Check specifically for raft.db with non-trivial content.
	// Previously this checked for ANY file in the data dir, which was too broad —
	// auto-discovery creates peers.json and log files before RQLite starts,
	// causing false positives that skip the -join flag on restart.
	raftDB := filepath.Join(rqliteDataDir, "raft.db")
	info, err := os.Stat(raftDB)
	if err != nil {
		return false
	}
	return info.Size() > 1024
}

func (r *RQLiteManager) exponentialBackoff(attempt int, baseDelay time.Duration, maxDelay time.Duration) time.Duration {
	delay := baseDelay * time.Duration(1<<uint(attempt))
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}
