package inspector

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
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
// Requires node.SSHKey to be set (via PrepareNodeKeys).
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

	if node.SSHKey == "" {
		return SSHResult{
			Duration: 0,
			Err:      fmt.Errorf("no SSH key for %s (call PrepareNodeKeys first)", node.Name()),
		}
	}

	args := append([]string{"ssh", "-n"}, node.HostKeyOptions()...)
	args = append(args, node.sharedConnectionOptions()...)
	args = append(args,
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "ForwardAgent=no",
		"-i", node.SSHKey,
		fmt.Sprintf("%s@%s", node.User, node.Host),
		command,
	)

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

// sshControlPersist is how long a shared connection outlives its last
// session. A collection is a burst of sessions; the master must not outlive it
// by much because Collect also closes it explicitly.
const sshControlPersist = "30s"

// sharedConnectionOptions are the ssh -o arguments that make a node's sessions
// share one connection, or none when the node has no ControlDir.
//
// Setting up an SSH session is most of what a collector costs: on a CPU-starved
// VPS (60% steal) it took 1-4s per session against 0.4s over an open
// connection, so the 14 sessions of one node's collection took 49s, longer
// than the whole --timeout. %C is a hash of the connection, so the path stays
// inside the 104 bytes of a unix socket name whatever the host is called.
func (n Node) sharedConnectionOptions() []string {
	if n.ControlDir == "" {
		return nil
	}
	return []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + filepath.Join(n.ControlDir, "%C"),
		"-o", "ControlPersist=" + sshControlPersist,
	}
}

// closeSharedConnection stops the node's connection master. An error is not
// reported: a master that is already gone (it idled out, or the node never
// answered) is exactly the state this wants.
func closeSharedConnection(node Node) {
	if node.ControlDir == "" {
		return
	}
	_ = exec.Command("ssh", "-O", "exit", "-o", "ControlPath="+filepath.Join(node.ControlDir, "%C"),
		fmt.Sprintf("%s@%s", node.User, node.Host)).Run()
}

// isSSHConnectionError returns true if the failure looks like an SSH connection
// problem (timeout, refused, network unreachable) rather than a remote command error.
func isSSHConnectionError(r SSHResult) bool {
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
