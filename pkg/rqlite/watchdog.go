package rqlite

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	watchdogInterval   = 30 * time.Second
	watchdogMaxRestart = 3
)

// startProcessWatchdog monitors the RQLite child process and restarts it if it crashes.
// It checks both process liveness and HTTP responsiveness.
func (r *RQLiteManager) startProcessWatchdog(ctx context.Context) {
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
			} else {
				// Process is alive — check HTTP responsiveness
				if !r.isHTTPResponsive() {
					r.logger.Warn("RQLite process is alive but not responding to HTTP")
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
