package rqlite

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	defaultBackupInterval = 1 * time.Hour
	maxBackupRetention    = 24
	backupDirName         = "backups/rqlite"
	backupPrefix          = "rqlite-backup-"
	backupSuffix          = ".db"
	backupTimestampFormat = "20060102-150405"
)

// startBackupLoop runs a periodic backup of the RQLite database.
// It saves consistent SQLite snapshots to the local backup directory.
// Only the leader node performs backups; followers skip silently.
func (r *RQLiteManager) startBackupLoop(ctx context.Context) {
	interval := r.config.BackupInterval
	if interval <= 0 {
		interval = defaultBackupInterval
	}

	r.logger.Info("RQLite backup loop started",
		zap.Duration("interval", interval),
		zap.Int("max_retention", maxBackupRetention))

	// Wait before the first backup to let the cluster stabilize
	select {
	case <-ctx.Done():
		return
	case <-time.After(interval):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run the first backup immediately after the initial wait
	r.performBackup()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("RQLite backup loop stopped")
			return
		case <-ticker.C:
			r.performBackup()
		}
	}
}

// performBackup executes a single backup cycle: check leadership, take snapshot, prune old backups.
func (r *RQLiteManager) performBackup() {
	// Only the leader should perform backups to avoid duplicate work
	if !r.isLeaderNode() {
		r.logger.Debug("Skipping backup: this node is not the leader")
		return
	}

	backupDir := r.backupDir()
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		r.logger.Error("Failed to create backup directory",
			zap.String("dir", backupDir),
			zap.Error(err))
		return
	}

	timestamp := time.Now().UTC().Format(backupTimestampFormat)
	filename := fmt.Sprintf("%s%s%s", backupPrefix, timestamp, backupSuffix)
	backupPath := filepath.Join(backupDir, filename)

	if err := r.downloadBackup(backupPath); err != nil {
		r.logger.Error("Failed to download RQLite backup",
			zap.String("path", backupPath),
			zap.Error(err))
		// Clean up partial file
		_ = os.Remove(backupPath)
		return
	}

	info, err := os.Stat(backupPath)
	if err != nil {
		r.logger.Error("Failed to stat backup file",
			zap.String("path", backupPath),
			zap.Error(err))
		return
	}

	r.logger.Info("RQLite backup completed",
		zap.String("path", backupPath),
		zap.Int64("size_bytes", info.Size()))

	r.pruneOldBackups(backupDir)
}

// isLeaderNode checks whether this node is currently the Raft leader.
func (r *RQLiteManager) isLeaderNode() bool {
	status, err := r.getRQLiteStatus()
	if err != nil {
		r.logger.Debug("Cannot determine leader status, skipping backup", zap.Error(err))
		return false
	}
	return status.Store.Raft.State == "Leader"
}

// backupDir returns the path to the backup directory.
func (r *RQLiteManager) backupDir() string {
	return filepath.Join(r.dataDir, backupDirName)
}

// downloadBackup calls the RQLite backup API and writes the SQLite snapshot to disk.
func (r *RQLiteManager) downloadBackup(destPath string) error {
	url := fmt.Sprintf("http://localhost:%d/db/backup", r.config.RQLitePort)
	client := &http.Client{Timeout: 2 * time.Minute}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("request backup endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("backup endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	outFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	defer outFile.Close()

	written, err := io.Copy(outFile, resp.Body)
	if err != nil {
		return fmt.Errorf("write backup data: %w", err)
	}

	if written == 0 {
		return fmt.Errorf("backup file is empty")
	}

	return nil
}

// pruneOldBackups removes the oldest backup files, keeping only the most recent maxBackupRetention.
func (r *RQLiteManager) pruneOldBackups(backupDir string) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		r.logger.Error("Failed to list backup directory for pruning",
			zap.String("dir", backupDir),
			zap.Error(err))
		return
	}

	// Collect only backup files matching our naming convention
	var backupFiles []os.DirEntry
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), backupPrefix) && strings.HasSuffix(entry.Name(), backupSuffix) {
			backupFiles = append(backupFiles, entry)
		}
	}

	if len(backupFiles) <= maxBackupRetention {
		return
	}

	// Sort by name ascending (timestamp in name ensures chronological order)
	sort.Slice(backupFiles, func(i, j int) bool {
		return backupFiles[i].Name() < backupFiles[j].Name()
	})

	// Remove the oldest files beyond the retention limit
	toDelete := backupFiles[:len(backupFiles)-maxBackupRetention]
	for _, entry := range toDelete {
		path := filepath.Join(backupDir, entry.Name())
		if err := os.Remove(path); err != nil {
			r.logger.Warn("Failed to delete old backup",
				zap.String("path", path),
				zap.Error(err))
		} else {
			r.logger.Debug("Pruned old backup", zap.String("path", path))
		}
	}

	r.logger.Info("Pruned old backups",
		zap.Int("deleted", len(toDelete)),
		zap.Int("remaining", maxBackupRetention))
}
