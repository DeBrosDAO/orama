package provision

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

const (
	// cmdTailBytes is how much of a failed command's output its error quotes.
	cmdTailBytes = 4096
	// logFileMode is the mode of every command log.
	logFileMode = 0o600
	// e2eFlag marks the orama CLI as running inside an e2e run (see
	// core/pkg/rwagent/e2eguard.go).
	e2eFlag = "ORAMA_E2E=1"
)

// credentials are the tokens Down, Sweep and the extras need.
type credentials struct {
	hetznerToken, cfToken, cfZone string
}

func (c Config) credentials() credentials {
	return credentials{hetznerToken: c.HetznerToken, cfToken: c.CFToken, cfZone: c.CFZone}
}

// credentialsFromEnv reads the tokens, naming only what is missing.
func credentialsFromEnv() (credentials, error) {
	c := credentials{
		hetznerToken: strings.TrimSpace(secrets.Getenv(EnvHetznerToken)),
		cfToken:      strings.TrimSpace(secrets.Getenv(EnvCFToken)),
		cfZone:       strings.TrimSpace(secrets.Getenv(EnvCFZone)),
	}
	var missing []string
	for name, v := range map[string]string{EnvHetznerToken: c.hetznerToken, EnvCFToken: c.cfToken, EnvCFZone: c.cfZone} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return credentials{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

// command is one local program run. env is its whole environment: nothing
// is inherited, so no token reaches a child that does not need it.
type command struct {
	name  string
	args  []string
	dir   string
	env   []string
	stdin []byte
	// log is the file that receives stdout and stderr; empty for none.
	log string
}

func (c command) String() string { return c.name + " " + strings.Join(c.args, " ") }

// commander runs local programs and returns their stdout.
type commander interface {
	Run(ctx context.Context, c command) (string, error)
}

// cmdError is a command that ran and failed.
type cmdError struct {
	cmd  string
	exit int
	tail string
	err  error
}

func (e *cmdError) Error() string {
	return fmt.Sprintf("`%s` failed (exit %d): %v: %s", e.cmd, e.exit, e.err, strings.TrimSpace(e.tail))
}

func (e *cmdError) Unwrap() error { return e.err }

// exitCode is a failed command's exit status, or -1.
func exitCode(err error) int {
	var ce *cmdError
	if errors.As(err, &ce) {
		return ce.exit
	}
	return -1
}

type execCommander struct{}

func (execCommander) Run(ctx context.Context, c command) (string, error) {
	cmd := exec.CommandContext(ctx, c.name, c.args...)
	cmd.Dir, cmd.Env = c.dir, c.env
	if c.stdin != nil {
		cmd.Stdin = bytes.NewReader(c.stdin)
	}
	var out bytes.Buffer
	tail := &tailWriter{limit: cmdTailBytes}
	var logW io.Writer = io.Discard
	if c.log != "" {
		f, err := os.OpenFile(c.log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, logFileMode)
		if err != nil {
			return "", fmt.Errorf("failed to open the command log %s: %w", c.log, err)
		}
		defer f.Close()
		logW = f
	}
	cmd.Stdout = io.MultiWriter(&out, tail, logW)
	cmd.Stderr = io.MultiWriter(tail, logW)
	if err := cmd.Run(); err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return out.String(), &cmdError{cmd: c.String(), exit: code, tail: tail.String(), err: err}
	}
	return out.String(), nil
}

// tailWriter keeps the last limit bytes; stdout and stderr write to it from
// two goroutines.
type tailWriter struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
