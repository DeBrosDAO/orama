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
	// redact is applied to the log and to the output an error quotes; nil
	// redacts the environment's secrets and the known credential shapes.
	redact func(string) string
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
	red := c.redact
	if red == nil {
		red = secrets.FromEnv(secrets.LookupEnv).Redact
	}
	var out bytes.Buffer
	tail := &tailWriter{limit: cmdTailBytes}
	logW, closeLog, err := openCommandLog(c.log, red)
	if err != nil {
		return "", err
	}
	cmd.Stdout = io.MultiWriter(&out, tail, logW)
	cmd.Stderr = io.MultiWriter(tail, logW)
	runErr := cmd.Run()
	if err := closeLog(); err != nil {
		runErr = errors.Join(runErr, err)
	}
	if runErr != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			code = ee.ExitCode()
		}
		return out.String(), &cmdError{cmd: red(c.String()), exit: code, tail: red(tail.String()), err: runErr}
	}
	return out.String(), nil
}

// openCommandLog opens the command log at path (none when empty) behind a
// writer that redacts it line by line; the returned func flushes and
// closes it.
func openCommandLog(path string, red func(string) string) (io.Writer, func() error, error) {
	if path == "" {
		return io.Discard, func() error { return nil }, nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, logFileMode)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open the command log %s: %w", path, err)
	}
	w := secrets.NewRedactingWriter(f, red)
	return w, func() error {
		if err := errors.Join(w.Close(), f.Close()); err != nil {
			return fmt.Errorf("failed to finish the command log %s: %w", path, err)
		}
		return nil
	}, nil
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
