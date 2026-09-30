package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/rwagent"
)

// addressPattern is an EVM address.
var addressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// homeFlag names the agent directory on rw-agent-headless's command line;
// StopDir recognises the agent by it.
const homeFlag = "--home"

// readyFile is what rw-agent-headless writes once it serves.
type readyFile struct {
	PID     int    `json:"pid"`
	Socket  string `json:"socket"`
	Address string `json:"address"`
}

// launch starts rw-agent-headless on dir in its own session, with its
// stdout and stderr going straight to a log file: no pipe of this process is
// in between, so the agent outlives the process that started it (a write to
// a pipe nobody reads any more would kill it with SIGPIPE), and a Ctrl-C to
// the starter's process group does not reach it.
func launch(cfg StartConfig, dir string) (*Agent, error) {
	a := &Agent{Dir: dir, Sock: SockPath(dir), exited: make(chan struct{})}
	if err := a.openLog(cfg.Log); err != nil {
		return nil, err
	}
	args := []string{homeFlag, dir, "--socket", a.Sock, "--password-file", filepath.Join(dir, passwordName),
		"--ready-file", filepath.Join(dir, readyName)}
	for _, ap := range cfg.Approvals {
		args = append(args, "--approve", ap.Binary+"="+strings.Join(ap.Caps, ","))
	}
	cmd := exec.Command(cfg.AgentBin, args...)
	cmd.Env = baseEnv(dir)
	cmd.Stdout, cmd.Stderr = a.log, a.log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(fmt.Errorf("failed to start %s: %w", cfg.AgentBin, err), a.closeLog())
	}
	a.cmd, a.PID = cmd, cmd.Process.Pid
	go func() {
		a.waitErr = cmd.Wait()
		close(a.exited)
	}()
	return a, nil
}

// openLog takes log as the agent's output file, or creates agentLogName in
// the agent directory when there is none, and remembers where the agent's
// output starts in it.
func (a *Agent) openLog(log *os.File) error {
	if log == nil {
		path := filepath.Join(a.Dir, agentLogName)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, secretFileMode)
		if err != nil {
			return fmt.Errorf("failed to create the agent log %s: %w", path, err)
		}
		log, a.ownsLog = f, true
	}
	fi, err := log.Stat()
	if err != nil {
		return errors.Join(fmt.Errorf("failed to stat the agent log %s: %w", log.Name(), err), a.closeLog())
	}
	a.log, a.logStart = log, fi.Size()
	return nil
}

// closeLog closes the log file launch created; a caller's file is the
// caller's to close.
func (a *Agent) closeLog() error {
	if !a.ownsLog || a.log == nil {
		return nil
	}
	if err := a.log.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("failed to close the agent log %s: %w", a.log.Name(), err)
	}
	return nil
}

// output is the last tailBytes the agent wrote to its log, read back from
// the file.
func (a *Agent) output() string {
	if a.log == nil {
		return ""
	}
	f, err := os.OpenFile(a.log.Name(), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Sprintf("(failed to read the agent log %s: %v)", a.log.Name(), err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Sprintf("(failed to stat the agent log %s: %v)", a.log.Name(), err)
	}
	from := min(max(a.logStart, fi.Size()-tailBytes), fi.Size())
	buf := make([]byte, fi.Size()-from)
	n, err := f.ReadAt(buf, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Sprintf("(failed to read the agent log %s: %v)", a.log.Name(), err)
	}
	return string(buf[:n])
}

// waitReady polls for the ready file until timeout, failing at once if the
// agent exits first.
func (a *Agent) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastErr := error(fs.ErrNotExist)
	for {
		rf, err := readReady(a.Dir)
		if err == nil {
			return a.acceptReady(rf)
		}
		// A ready file caught half-written reads as bad JSON; it is polled
		// again until the deadline, and the last reason is reported.
		lastErr = err
		select {
		case <-a.exited:
			return a.exitError()
		case <-deadline.C:
			return fmt.Errorf("rw-agent-headless was not ready in %s (%v); its output: %s", timeout, lastErr, a.output())
		case <-ctx.Done():
			return fmt.Errorf("waiting for rw-agent-headless: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// exitError explains an agent that exited before it was ready, naming the
// RootWallet task when it refused a capability.
func (a *Agent) exitError() error {
	out := strings.TrimSpace(a.output())
	err := fmt.Errorf("rw-agent-headless exited before it was ready (%v): %s", a.waitErr, out)
	lower := strings.ToLower(out)
	if strings.Contains(lower, "capabilit") || strings.Contains(lower, "approve") {
		return fmt.Errorf("%w — the agent refused a pre-approval; pre-approving %s and %s on a headless agent is %s",
			err, CapWalletSignArchive, CapWalletSignOramaTx, RootWalletHeadlessTask)
	}
	return err
}

func readReady(dir string) (readyFile, error) {
	var rf readyFile
	raw, err := os.ReadFile(filepath.Join(dir, readyName))
	if err != nil {
		return rf, err
	}
	if err := json.Unmarshal(raw, &rf); err != nil {
		return rf, fmt.Errorf("rw-agent-headless ready file is not JSON: %w", err)
	}
	return rf, nil
}

// acceptReady checks the ready file describes this agent.
func (a *Agent) acceptReady(rf readyFile) error {
	if rf.Socket != a.Sock {
		return fmt.Errorf("rw-agent-headless is ready on %q, not the socket it was given (%s)", rf.Socket, a.Sock)
	}
	if rf.PID != a.PID {
		return fmt.Errorf("the ready file names pid %d, but the agent started is pid %d", rf.PID, a.PID)
	}
	if !addressPattern.MatchString(rf.Address) {
		return fmt.Errorf("rw-agent-headless reports address %q, which is not an EVM address", rf.Address)
	}
	a.Address = rf.Address
	return nil
}

// checkStatus asks the agent over its socket whether it is unlocked.
func (a *Agent) checkStatus(ctx context.Context) error {
	sctx, cancel := context.WithTimeout(ctx, DefaultReadyTimeout)
	defer cancel()
	st, err := rwagent.New(a.Sock).Status(sctx)
	if err != nil {
		return fmt.Errorf("the test agent does not answer GET /v1/status on %s: %w", a.Sock, err)
	}
	if st.Locked {
		return errors.New("the test agent reports its wallet locked right after start: check the password file handling")
	}
	return nil
}

// Stop ends the agent (SIGTERM, then SIGKILL after stopTimeout) and shreds
// its directory. It is safe to call more than once.
func (a *Agent) Stop() error {
	a.stopOnce.Do(func() {
		var termErr error
		if a.cmd != nil {
			termErr = a.terminate()
		}
		a.stopErr = errors.Join(termErr, a.closeLog(), shredTree(a.Dir))
	})
	return a.stopErr
}

func (a *Agent) terminate() error {
	select {
	case <-a.exited:
		return nil
	default:
	}
	if err := a.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("failed to signal the test agent (pid %d): %w", a.PID, err)
	}
	select {
	case <-a.exited:
		return nil
	case <-time.After(stopTimeout):
	}
	killErr := a.cmd.Process.Kill()
	<-a.exited
	return errors.Join(fmt.Errorf("the test agent (pid %d) ignored SIGTERM for %s and was killed", a.PID, stopTimeout), killErr)
}

// tail keeps the last limit bytes written.
type tail struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
