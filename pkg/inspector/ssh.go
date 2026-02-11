package inspector

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	sshMaxRetries = 3
	sshRetryDelay = 2 * time.Second
)

// SSHResult holds the output of an SSH command execution.
type SSHResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	Err      error
	Retries  int // how many retries were needed
}

// OK returns true if the command succeeded (exit code 0, no error).
func (r SSHResult) OK() bool {
	return r.Err == nil && r.ExitCode == 0
}

// RunSSH executes a command on a remote node via SSH with retry on connection failure.
// Uses sshpass for password auth, falls back to -i for key-based auth.
// The -n flag is used to prevent SSH from reading stdin.
func RunSSH(ctx context.Context, node Node, command string) SSHResult {
	var result SSHResult
	for attempt := 0; attempt <= sshMaxRetries; attempt++ {
		result = runSSHOnce(ctx, node, command)
		result.Retries = attempt

		// Success — return immediately
		if result.OK() {
			return result
		}

		// If the command ran but returned non-zero exit, that's the remote command
		// failing (not a connection issue) — don't retry
		if result.Err == nil && result.ExitCode != 0 {
			return result
		}

		// Check if it's a connection-level failure worth retrying
		if !isSSHConnectionError(result) {
			return result
		}

		// Don't retry if context is done
		if ctx.Err() != nil {
			return result
		}

		// Wait before retry (except on last attempt)
		if attempt < sshMaxRetries {
			select {
			case <-time.After(sshRetryDelay):
			case <-ctx.Done():
				return result
			}
		}
	}
	return result
}

// runSSHOnce executes a single SSH attempt.
func runSSHOnce(ctx context.Context, node Node, command string) SSHResult {
	start := time.Now()

	var args []string
	if node.SSHKey != "" {
		// Key-based auth
		args = []string{
			"ssh", "-n",
			"-o", "StrictHostKeyChecking=no",
			"-o", "ConnectTimeout=10",
			"-o", "BatchMode=yes",
			"-i", node.SSHKey,
			fmt.Sprintf("%s@%s", node.User, node.Host),
			command,
		}
	} else {
		// Password auth via sshpass
		args = []string{
			"sshpass", "-p", node.Password,
			"ssh", "-n",
			"-o", "StrictHostKeyChecking=no",
			"-o", "ConnectTimeout=10",
			fmt.Sprintf("%s@%s", node.User, node.Host),
			command,
		}
	}

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			}
		}
	}

	return SSHResult{
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		ExitCode: exitCode,
		Duration: duration,
		Err:      err,
	}
}

// isSSHConnectionError returns true if the failure looks like an SSH connection
// problem (timeout, refused, network unreachable) rather than a remote command error.
func isSSHConnectionError(r SSHResult) bool {
	// sshpass exit code 5 = invalid/incorrect password (not retriable)
	// sshpass exit code 6 = host key verification failed (not retriable)
	// SSH exit code 255 = SSH connection error (retriable)
	if r.ExitCode == 255 {
		return true
	}

	stderr := strings.ToLower(r.Stderr)
	connectionErrors := []string{
		"connection refused",
		"connection timed out",
		"connection reset",
		"no route to host",
		"network is unreachable",
		"could not resolve hostname",
		"ssh_exchange_identification",
		"broken pipe",
		"connection closed by remote host",
	}
	for _, pattern := range connectionErrors {
		if strings.Contains(stderr, pattern) {
			return true
		}
	}
	return false
}

// RunSSHMulti executes a multi-command string on a remote node.
// Commands are joined with " && " so failure stops execution.
func RunSSHMulti(ctx context.Context, node Node, commands []string) SSHResult {
	combined := strings.Join(commands, " && ")
	return RunSSH(ctx, node, combined)
}
