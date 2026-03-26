package rqlite

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	// watchdogInterval is how often we check if rqlited is alive.
	watchdogInterval = 30 * time.Second

	// watchdogMaxRestart is the maximum number of restart attempts before giving up.
	watchdogMaxRestart = 3

	// watchdogGracePeriod is how long to wait after a restart before
	// the watchdog starts checking. This gives rqlited time to rejoin
	// the Raft cluster — Raft election timeouts + log replay can take
	// 60-120 seconds after a restart.
	watchdogGracePeriod = 120 * time.Second
)

// startProcessWatchdog monitors the RQLite child process and restarts it if it crashes.
// It only restarts when the process has actually DIED (exited). It does NOT kill
// rqlited for being slow to find a leader — that's normal during cluster rejoin.
func (r *RQLiteManager) startProcessWatchdog(ctx context.Context) {
	// Wait for the grace period before starting to monitor.
	// rqlited needs time to:
	// 1. Open the raft log and snapshots
	// 2. Reconnect to existing Raft peers
	// 3. Either rejoin as follower or participate in a new election
	select {
	case <-ctx.Done():
		return
	case <-time.After(watchdogGracePeriod):
	}

	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()

	restartCount := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !r.isProcessAlive() {
				r.logger.Error("RQLite process has died",
					zap.Int("restart_count", restartCount),
					zap.Int("max_restarts", watchdogMaxRestart))

				if restartCount >= watchdogMaxRestart {
					r.logger.Error("RQLite process watchdog: max restart attempts reached, giving up")
					return
				}

				if err := r.restartProcess(ctx); err != nil {
					r.logger.Error("Failed to restart RQLite process", zap.Error(err))
					restartCount++
					continue
				}

				restartCount++
				r.logger.Info("RQLite process restarted by watchdog",
					zap.Int("restart_count", restartCount))

				// Give the restarted process time to stabilize before checking again
				select {
				case <-ctx.Done():
					return
				case <-time.After(watchdogGracePeriod):
				}
			} else {
				// Process is alive — reset restart counter on sustained health
				if r.isHTTPResponsive() {
					if restartCount > 0 {
						r.logger.Info("RQLite process has stabilized, resetting restart counter",
							zap.Int("previous_restart_count", restartCount))
						restartCount = 0
					}
				}
			}
		}
	}
}

// isProcessAlive checks if the RQLite child process is still running
func (r *RQLiteManager) isProcessAlive() bool {
	if r.cmd == nil || r.cmd.Process == nil {
		return false
	}
	// On Unix, sending signal 0 checks process existence without actually signaling
	if err := r.cmd.Process.Signal(nil); err != nil {
		return false
	}
	return true
}

// isHTTPResponsive checks if RQLite is responding to HTTP status requests
func (r *RQLiteManager) isHTTPResponsive() bool {
	url := fmt.Sprintf("http://localhost:%d/status", r.config.RQLitePort)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// restartProcess attempts to restart the RQLite process
func (r *RQLiteManager) restartProcess(ctx context.Context) error {
	rqliteDataDir, err := r.rqliteDataDirPath()
	if err != nil {
		return fmt.Errorf("get data dir: %w", err)
	}

	if err := r.launchProcess(ctx, rqliteDataDir); err != nil {
		return fmt.Errorf("launch process: %w", err)
	}

	if err := r.waitForReadyAndConnect(ctx); err != nil {
		return fmt.Errorf("wait for ready: %w", err)
	}

	return nil
}
