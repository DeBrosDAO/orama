package onionnet

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// BootstrapTimeout is how long a client waits for tor to build its first
	// circuit through the network. A private network's consensus is fetched
	// from its authorities, so a network that is down shows up here.
	BootstrapTimeout = 3 * time.Minute

	bootstrapDone  = "Bootstrapped 100%"
	torrcName      = "torrc"
	defaultsName   = "torrc-defaults"
	dataDirMode    = 0o700
	torrcMode      = 0o600
	stopGrace      = 10 * time.Second
	logTailLines   = 12
	torLogLineSize = 64 << 10
)

// Tor is a running client tor.
type Tor struct {
	// SocksAddr is the loopback address tor accepts SOCKS5 on.
	SocksAddr string
	// DNSAddr is where tor answers DNS, empty when it does not.
	DNSAddr string

	cmd  *exec.Cmd
	done chan struct{}
	// stopping is set once this client asked tor to stop, by Stop or by its
	// context ending; tor's exit after that is not an error.
	stopping atomic.Bool
	err      error // the exit status, valid once done is closed
}

// Done is closed when tor has exited.
func (t *Tor) Done() <-chan struct{} { return t.done }

// Err is why tor exited; nil on a stop this client asked for. Valid after Done.
func (t *Tor) Err() error { return t.err }

// Stop ends tor and waits for it. It is safe to call twice.
func (t *Tor) Stop() error {
	t.stopping.Store(true)
	if t.cmd.Process != nil {
		_ = terminate(t.cmd.Process)
	}
	select {
	case <-t.done:
	case <-time.After(stopGrace):
		_ = t.cmd.Process.Kill()
		<-t.done
	}
	return nil
}

// Start writes the network's torrc into opts.DataDir, runs the tor binary at bin
// on it, and returns once tor reports it has bootstrapped, which needs the
// network's authorities to answer. ctx ends tor. Tor's log goes to logw when
// it is not nil. When tor exits before bootstrapping, the error carries the
// tail of its log.
func Start(ctx context.Context, bin string, n Network, opts Options, logw io.Writer) (*Tor, error) {
	torrc, err := n.Torrc(opts)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opts.DataDir, dataDirMode); err != nil {
		return nil, fmt.Errorf("create the tor data directory: %w", err)
	}
	path := filepath.Join(opts.DataDir, torrcName)
	if err := os.WriteFile(path, []byte(torrc), torrcMode); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	// An empty defaults file replaces the one the distribution's tor package
	// reads, so nothing but this torrc configures the client.
	defaults := filepath.Join(opts.DataDir, defaultsName)
	if err := os.WriteFile(defaults, nil, torrcMode); err != nil {
		return nil, fmt.Errorf("write %s: %w", defaults, err)
	}
	t := &Tor{SocksAddr: opts.SocksAddr, DNSAddr: opts.DNSAddr, done: make(chan struct{})}
	cmd := exec.CommandContext(ctx, bin, "-f", path, "--defaults-torrc", defaults)
	cmd.Cancel = func() error {
		t.stopping.Store(true)
		return terminate(cmd.Process)
	}
	cmd.WaitDelay = stopGrace
	t.cmd = cmd
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("tor output: %w", err)
	}
	safe := &lockedWriter{w: logw}
	cmd.Stderr = safe
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start tor (%s): %w", bin, err)
	}
	boot := make(chan struct{})
	tail := &logTail{}
	go t.pump(out, safe, tail, boot)
	select {
	case <-boot:
		return t, nil
	case <-t.done:
		return nil, fmt.Errorf("tor exited before it bootstrapped: %w\n%s", t.err, tail.String())
	case <-time.After(BootstrapTimeout):
		_ = t.Stop()
		return nil, fmt.Errorf("tor did not bootstrap in %s; are the network's authorities reachable?\n%s", BootstrapTimeout, tail.String())
	case <-ctx.Done():
		_ = t.Stop()
		return nil, fmt.Errorf("stopped while tor was bootstrapping: %w", ctx.Err())
	}
}

// StartOnFreePort is Start for a client that has no fixed port to offer: tor
// listens on a loopback port found for the run, and keeps its state in dataDir
// (the network's directory under the user cache directory when empty).
func StartOnFreePort(ctx context.Context, bin string, n Network, dataDir string, logw io.Writer) (*Tor, error) {
	if dataDir == "" {
		var err error
		if dataDir, err = DefaultDataDir(n.Name); err != nil {
			return nil, err
		}
	}
	socks, err := FreeLoopbackAddr()
	if err != nil {
		return nil, err
	}
	return Start(ctx, bin, n, Options{DataDir: dataDir, SocksAddr: socks}, logw)
}

// pump copies tor's log, notes the bootstrap, and records how tor ended.
func (t *Tor) pump(out io.Reader, logw *lockedWriter, tail *logTail, boot chan<- struct{}) {
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 4096), torLogLineSize)
	var once sync.Once
	for sc.Scan() {
		line := sc.Text()
		tail.add(line)
		fmt.Fprintln(logw, line)
		if strings.Contains(line, bootstrapDone) {
			once.Do(func() { close(boot) })
		}
	}
	// A log line longer than the scanner takes ends the loop above; keep reading so
	// tor never blocks on a full pipe.
	_, _ = io.Copy(io.Discard, out)
	err := t.cmd.Wait()
	if t.stopping.Load() {
		err = nil
	}
	t.err = err
	close(t.done)
}

// logTail keeps the last lines tor wrote, for an error message.
type logTail struct {
	mu    sync.Mutex
	lines []string
}

func (l *logTail) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, s)
	if len(l.lines) > logTailLines {
		l.lines = l.lines[1:]
	}
}

func (l *logTail) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// FreeLoopbackAddr returns a loopback "ip:port" nothing was listening on a
// moment ago, for a listener tor is about to bind. If something takes it first
// tor fails to bind and Start reports that.
func FreeLoopbackAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("find a free loopback port: %w", err)
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

// terminate asks tor to shut down: SIGTERM, which tor answers with a clean exit.
// Windows has no SIGTERM, so tor is killed there.
func terminate(p *os.Process) error {
	if runtime.GOOS == "windows" {
		return p.Kill()
	}
	return p.Signal(syscall.SIGTERM)
}

// lockedWriter serialises the two streams tor's log arrives on, and drops
// them when no writer was given.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	if l.w == nil {
		return len(p), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
