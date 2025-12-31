package rqlite

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (r *RQLiteManager) rqliteDataDirPath() (string, error) {
	dataDir := os.ExpandEnv(r.dataDir)
	if strings.HasPrefix(dataDir, "~") {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, dataDir[1:])
	}
	return filepath.Join(dataDir, "rqlite"), nil
}

func (r *RQLiteManager) resolveMigrationsDir() (string, error) {
	productionPath := "/home/debros/src/migrations"
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
	entries, err := os.ReadDir(rqliteDataDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != "." && e.Name() != ".." {
			return true
		}
	}
	return false
}

func (r *RQLiteManager) exponentialBackoff(attempt int, baseDelay time.Duration, maxDelay time.Duration) time.Duration {
	delay := baseDelay * time.Duration(1<<uint(attempt))
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

