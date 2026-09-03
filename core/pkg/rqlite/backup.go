package rqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	defaultBackupInterval = 1 * time.Hour

	// Retention, local and pinned. Three hourly files on one node's disk is
	// not a history: it covers three hours, and only the hours that node
	// happened to be leader for.
	hourlyRetention       = 24
	dailyRetention        = 7
	backupDirName         = "backups/rqlite"
	backupPrefix          = "rqlite-backup-"
	backupSuffix          = ".db"
	backupTimestampFormat = "20060102-150405"

	// backupPushTimeout bounds the encrypt-add-pin-record round trip.
	backupPushTimeout = 5 * time.Minute
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
		zap.Int("hourly_retention", hourlyRetention),
		zap.Int("daily_retention", dailyRetention))

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
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		r.logger.Error("Failed to create backup directory",
			zap.String("dir", backupDir),
			zap.Error(err))
		return
	}
	_ = os.Chmod(backupDir, 0700)

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

	// Push it off the box. A snapshot that exists only on the machine that
	// made it protects against nothing that actually happens — the disk fails,
	// the VPS is deleted, a wipe removes it, or leadership moves and the series
	// fragments across nodes.
	//
	// Logged at Error, because "the backup did not leave the node" is exactly
	// the thing nobody knew.
	pushCtx, cancel := context.WithTimeout(context.Background(), backupPushTimeout)
	db, dbErr := r.localSQLHandle()
	if dbErr != nil {
		r.logger.Error("Registry backup stayed on this node: no database handle to record it",
			zap.String("path", backupPath), zap.Error(dbErr))
	} else if err := r.pushBackupOffBox(pushCtx, backupPath, db); err != nil {
		r.logger.Error("Registry backup stayed on this node's disk",
			zap.String("path", backupPath), zap.Error(err))
	}
	cancel()

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

// downloadBackup calls the RQLite backup API and writes the SQLite snapshot to
// disk, through the admin client so it carries credentials.
func (r *RQLiteManager) downloadBackup(destPath string) error {
	data, err := r.LocalAdminClient().Backup(context.Background())
	if err != nil {
		return fmt.Errorf("request backup endpoint: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("backup file is empty")
	}
	if err := os.WriteFile(destPath, data, 0600); err != nil {
		return fmt.Errorf("write backup data: %w", err)
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

	// Sort by name ascending (timestamp in name ensures chronological order)
	sort.Slice(backupFiles, func(i, j int) bool {
		return backupFiles[i].Name() < backupFiles[j].Name()
	})

	names := make([]string, 0, len(backupFiles))
	for _, e := range backupFiles {
		names = append(names, e.Name())
	}

	keep := backupsToKeep(names)
	var toDelete []os.DirEntry
	for _, entry := range backupFiles {
		if !keep[entry.Name()] {
			toDelete = append(toDelete, entry)
		}
	}
	if len(toDelete) == 0 {
		return
	}
	for _, entry := range toDelete {
		path := filepath.Join(backupDir, entry.Name())
		// No zero-overwrite before the delete. It shredded nothing —
		// journaled, copy-on-write and flash-translated storage all write the
		// zeros somewhere else — while costing 64 KB of I/O per file and
		// implying a guarantee that was never there.
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
		zap.Int("kept", len(backupFiles)-len(toDelete)))
}

// backupsToKeep decides which backup filenames survive a prune: the most recent
// hourlyRetention, plus one per day for dailyRetention days.
//
// A flat "keep the newest N" covers N hours and nothing else. What an operator
// needs from a registry backup is usually not the last hour — it is the state
// before a change that turned out to be wrong, which may be days back.
//
// Names are the timestamped backup filenames; anything that does not parse is
// kept, because deleting a file this cannot identify is the one outcome worth
// avoiding.
func backupsToKeep(names []string) map[string]bool {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	keep := make(map[string]bool, len(sorted))

	// Newest first.
	for i := len(sorted) - 1; i >= 0; i-- {
		name := sorted[i]
		if _, err := backupTimestamp(name); err != nil {
			keep[name] = true
		}
	}

	kept := 0
	for i := len(sorted) - 1; i >= 0 && kept < hourlyRetention; i-- {
		if _, err := backupTimestamp(sorted[i]); err != nil {
			continue
		}
		keep[sorted[i]] = true
		kept++
	}

	// One per day, for the oldest backup of each of the last dailyRetention
	// days: the earliest file of a day is the one closest to "before anything
	// happened that day".
	firstOfDay := map[string]string{}
	for _, name := range sorted {
		ts, err := backupTimestamp(name)
		if err != nil {
			continue
		}
		day := ts.Format("2006-01-02")
		if _, seen := firstOfDay[day]; !seen {
			firstOfDay[day] = name
		}
	}

	days := make([]string, 0, len(firstOfDay))
	for day := range firstOfDay {
		days = append(days, day)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	for i, day := range days {
		if i >= dailyRetention {
			break
		}
		keep[firstOfDay[day]] = true
	}

	return keep
}

// backupTimestamp parses the time out of a backup filename.
func backupTimestamp(name string) (time.Time, error) {
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, backupPrefix), backupSuffix)
	return time.Parse(backupTimestampFormat, stamp)
}
