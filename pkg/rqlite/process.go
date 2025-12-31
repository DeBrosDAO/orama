package rqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/tlsutil"
	"github.com/rqlite/gorqlite"
	"go.uber.org/zap"
)

// launchProcess starts the RQLite process with appropriate arguments
func (r *RQLiteManager) launchProcess(ctx context.Context, rqliteDataDir string) error {
	// Build RQLite command
	args := []string{
		"-http-addr", fmt.Sprintf("0.0.0.0:%d", r.config.RQLitePort),
		"-http-adv-addr", r.discoverConfig.HttpAdvAddress,
		"-raft-adv-addr", r.discoverConfig.RaftAdvAddress,
		"-raft-addr", fmt.Sprintf("0.0.0.0:%d", r.config.RQLiteRaftPort),
	}

	if r.config.NodeCert != "" && r.config.NodeKey != "" {
		r.logger.Info("Enabling node-to-node TLS encryption",
			zap.String("node_cert", r.config.NodeCert),
			zap.String("node_key", r.config.NodeKey))

		args = append(args, "-node-cert", r.config.NodeCert)
		args = append(args, "-node-key", r.config.NodeKey)

		if r.config.NodeCACert != "" {
			args = append(args, "-node-ca-cert", r.config.NodeCACert)
		}
		if r.config.NodeNoVerify {
			args = append(args, "-node-no-verify")
		}
	}

	if r.config.RQLiteJoinAddress != "" {
		r.logger.Info("Joining RQLite cluster", zap.String("join_address", r.config.RQLiteJoinAddress))

		joinArg := r.config.RQLiteJoinAddress
		if strings.HasPrefix(joinArg, "http://") {
			joinArg = strings.TrimPrefix(joinArg, "http://")
		} else if strings.HasPrefix(joinArg, "https://") {
			joinArg = strings.TrimPrefix(joinArg, "https://")
		}

		joinTimeout := 5 * time.Minute
		if err := r.waitForJoinTarget(ctx, r.config.RQLiteJoinAddress, joinTimeout); err != nil {
			r.logger.Warn("Join target did not become reachable within timeout; will still attempt to join",
				zap.Error(err))
		}

		args = append(args, "-join", joinArg, "-join-as", r.discoverConfig.RaftAdvAddress, "-join-attempts", "30", "-join-interval", "10s")
	}

	args = append(args, rqliteDataDir)

	r.cmd = exec.Command("rqlited", args...)

	nodeType := r.nodeType
	if nodeType == "" {
		nodeType = "node"
	}

	logsDir := filepath.Join(filepath.Dir(r.dataDir), "logs")
	_ = os.MkdirAll(logsDir, 0755)

	logPath := filepath.Join(logsDir, fmt.Sprintf("rqlite-%s.log", nodeType))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	r.cmd.Stdout = logFile
	r.cmd.Stderr = logFile

	if err := r.cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start RQLite: %w", err)
	}

	logFile.Close()
	return nil
}

// waitForReadyAndConnect waits for RQLite to be ready and establishes connection
func (r *RQLiteManager) waitForReadyAndConnect(ctx context.Context) error {
	if err := r.waitForReady(ctx); err != nil {
		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		return err
	}

	var conn *gorqlite.Connection
	var err error
	maxConnectAttempts := 10
	connectBackoff := 500 * time.Millisecond

	for attempt := 0; attempt < maxConnectAttempts; attempt++ {
		conn, err = gorqlite.Open(fmt.Sprintf("http://localhost:%d", r.config.RQLitePort))
		if err == nil {
			r.connection = conn
			break
		}

		if strings.Contains(err.Error(), "store is not open") {
			time.Sleep(connectBackoff)
			connectBackoff = time.Duration(float64(connectBackoff) * 1.5)
			if connectBackoff > 5*time.Second {
				connectBackoff = 5 * time.Second
			}
			continue
		}

		if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		return fmt.Errorf("failed to connect to RQLite: %w", err)
	}

	if conn == nil {
		return fmt.Errorf("failed to connect to RQLite after max attempts")
	}

	_ = r.validateNodeID()
	return nil
}

// waitForReady waits for RQLite to be ready to accept connections
func (r *RQLiteManager) waitForReady(ctx context.Context) error {
	url := fmt.Sprintf("http://localhost:%d/status", r.config.RQLitePort)
	client := tlsutil.NewHTTPClient(2 * time.Second)

	for i := 0; i < 180; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}

		resp, err := client.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			var statusResp map[string]interface{}
			if err := json.Unmarshal(body, &statusResp); err == nil {
				if raft, ok := statusResp["raft"].(map[string]interface{}); ok {
					state, _ := raft["state"].(string)
					if state == "leader" || state == "follower" {
						return nil
					}
				} else {
					return nil // Backwards compatibility
				}
			}
		}
	}

	return fmt.Errorf("RQLite did not become ready within timeout")
}

// waitForSQLAvailable waits until a simple query succeeds
func (r *RQLiteManager) waitForSQLAvailable(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if r.connection == nil {
				continue
			}
			_, err := r.connection.QueryOne("SELECT 1")
			if err == nil {
				return nil
			}
		}
	}
}

// testJoinAddress tests if a join address is reachable
func (r *RQLiteManager) testJoinAddress(joinAddress string) error {
	client := tlsutil.NewHTTPClient(5 * time.Second)
	var statusURL string
	if strings.HasPrefix(joinAddress, "http://") || strings.HasPrefix(joinAddress, "https://") {
		statusURL = strings.TrimRight(joinAddress, "/") + "/status"
	} else {
		host := joinAddress
		if idx := strings.Index(joinAddress, ":"); idx != -1 {
			host = joinAddress[:idx]
		}
		statusURL = fmt.Sprintf("http://%s:%d/status", host, 5001)
	}

	resp, err := client.Get(statusURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("leader returned status %d", resp.StatusCode)
	}
	return nil
}

// waitForJoinTarget waits until the join target's HTTP status becomes reachable
func (r *RQLiteManager) waitForJoinTarget(ctx context.Context, joinAddress string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		if err := r.testJoinAddress(joinAddress); err == nil {
			return nil
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return lastErr
}

