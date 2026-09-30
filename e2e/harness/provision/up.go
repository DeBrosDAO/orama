package provision

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/agent"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
	"golang.org/x/crypto/ssh"
)

// cleanupTimeout bounds the teardown of a failed Up, which runs even when
// Up's own context was cancelled.
const cleanupTimeout = 15 * time.Minute

// run is the state of one Up.
type run struct {
	cfg   Config
	log   Logger
	d     deps
	st    *fleet.State
	agent *agentRun
	// agentLog is the agent's output file; it is closed after the agent
	// stops when Up fails, and stays open (with the agent) when Up succeeds.
	agentLog *os.File
	// stateWritten is set once this run has saved its own state file: only
	// then may a failed Up remove the file (another run may own one there).
	stateWritten bool
	// goEnv is the Go toolchain's GOPATH, GOMODCACHE and GOCACHE.
	goEnv []string
	// hostKeys are the pinned SHA256 fingerprints by public IP.
	hostKeys map[string]string
	// serverKeys are the host keys generated for each server before it was
	// created, by fleet name.
	serverKeys map[string]ssh.PublicKey
	sshKeyID   int64
	firewallID int64
	// ownsCloud is set once this run registers anything labelled with its
	// id; before that, a label teardown would delete another run's servers.
	ownsCloud bool
	pubKey    string
	logSeq    atomic.Int32
	// red redacts every log and error of this Up: the environment's
	// secrets plus the test wallet's password and mnemonic.
	red *secrets.Redactor
}

// UpError is how Up fails: Owned reports whether the failed Up registered
// anything labelled with the run id. When it did not (a refused preflight,
// a run id already in use by another run), nothing may be torn down by the
// run's label: that would delete the other run's resources.
type UpError struct {
	Err   error
	Owned bool
}

func (e *UpError) Error() string { return e.Err.Error() }
func (e *UpError) Unwrap() error { return e.Err }

// OwnsResources reports whether a failed Up (its error, err) may have left
// resources labelled with its run id: only then may the caller tear the run
// down by label. An error that is not an *UpError came before Up created
// anything.
func OwnsResources(err error) bool {
	var ue *UpError
	return errors.As(err, &ue) && ue.Owned
}

// Up provisions the whole run and returns its state, written to
// StatePath(cfg.WorkDir). On any failure it tears down everything it created
// (as Down does) and returns an *UpError saying whether it owned anything.
func Up(ctx context.Context, cfg Config, log Logger) (*fleet.State, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	d, err := realDeps(cfg.credentials())
	if err != nil {
		return nil, err
	}
	return up(ctx, cfg, log, d)
}

func up(ctx context.Context, cfg Config, log Logger, d deps) (*fleet.State, error) {
	r := newRun(cfg, log, d)
	for _, p := range phases() {
		log.Infof("[%s] %s", p.name, strings.Join(p.plan(cfg), "; "))
		if err := p.exec(r, ctx); err != nil {
			failed := fmt.Errorf("provision %s failed in phase %s: %w", cfg.RunID, p.name, err)
			log.Infof("[%s] failed, tearing down what was created: %v", p.name, err)
			return nil, r.fail(ctx, failed)
		}
		if err := r.checkpoint(); err != nil {
			return nil, r.fail(ctx, err)
		}
	}
	return r.st, nil
}

func newRun(cfg Config, log Logger, d deps) *run {
	st := &fleet.State{
		RunID:       cfg.RunID,
		Env:         envName(cfg.RunID),
		ArtifactDir: cfg.ArtifactDir,
		OramaBin:    filepath.Join(cfg.WorkDir, binDir, oramaBinName),
	}
	return &run{cfg: cfg, log: log, d: d, st: st, hostKeys: map[string]string{}, serverKeys: map[string]ssh.PublicKey{},
		red: secrets.FromEnv(secrets.LookupEnv)}
}

// checkpoint saves the state once there is something to tear down, so a
// crash leaves a state file Down can use.
func (r *run) checkpoint() error {
	if r.st.Home == "" {
		return nil
	}
	return r.saveState()
}

// saveState writes the state file, creating it on the first save.
func (r *run) saveState() error {
	if err := saveState(r.st, StatePath(r.cfg.WorkDir), !r.stateWritten); err != nil {
		return err
	}
	r.stateWritten = true
	return nil
}

// fail cleans up after a failed phase and returns the *UpError, which
// carries whether this run registered anything under its label.
func (r *run) fail(ctx context.Context, err error) error {
	return &UpError{Err: errors.Join(err, r.cleanup(ctx)), Owned: r.ownsCloud}
}

// cleanup stops the agent and removes everything labelled with the run, on a
// context that survives the cancellation of Up's own.
func (r *run) cleanup(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	var errs []error
	if r.agent != nil {
		errs = append(errs, r.agent.Stop())
	}
	if r.agentLog != nil {
		if err := r.agentLog.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close the agent log %s: %w", r.agentLog.Name(), err))
		}
	}
	if r.ownsCloud {
		errs = append(errs, down(cctx, r.st, r.log, r.d))
	}
	if err := errors.Join(errs...); err != nil {
		r.log.Infof("teardown incomplete; %s is kept for Down to retry", StatePath(r.cfg.WorkDir))
		return err
	}
	if !r.stateWritten {
		return nil
	}
	return removeState(r.cfg.WorkDir)
}

// removeState deletes a torn-down run's state file, so the work dir can be
// used again. One never written is not an error.
func removeState(workDir string) error {
	if err := os.Remove(StatePath(workDir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to remove the torn-down run's state %s: %w", StatePath(workDir), err)
	}
	return nil
}

// writeState is the last phase.
func (r *run) writeState(context.Context) error {
	path := StatePath(r.cfg.WorkDir)
	if err := r.saveState(); err != nil {
		return err
	}
	r.log.Infof("run %s is up: %s, state in %s", r.cfg.RunID, r.st.GatewayURL, path)
	return nil
}

// cliEnv is the whole environment the orama CLI runs with: the test agent's
// HOME and socket, ORAMA_E2E=1, a private TMPDIR and the Go toolchain's
// caches. No token is in it.
func (r *run) cliEnv() []string {
	env := []string{"PATH=" + os.Getenv("PATH"), e2eFlag, "TMPDIR=" + filepath.Join(r.cfg.WorkDir, tmpDirName)}
	if r.agent != nil {
		env = append(env, "HOME="+r.agent.Dir, "RW_AGENT_SOCK="+r.agent.Sock)
	}
	return append(env, r.goEnv...)
}

// oramaCmd runs the CLI under test with args, logging to the artifacts.
func (r *run) oramaCmd(ctx context.Context, args ...string) (string, error) {
	return r.runLogged(ctx, command{name: r.st.OramaBin, args: args, env: r.cliEnv()})
}

// runLogged runs c with its output in a numbered log under the artifacts.
func (r *run) runLogged(ctx context.Context, c command) (string, error) {
	n := r.logSeq.Add(1)
	label := filepath.Base(c.name)
	if len(c.args) > 0 {
		label += "-" + c.args[0]
	}
	c.log = filepath.Join(r.cfg.ArtifactDir, fmt.Sprintf("provision-%02d-%s.log", n, sanitize(label)))
	c.redact = r.red.Redact
	return r.d.cmd.Run(ctx, c)
}

// sanitize keeps a log name to letters, digits and '-'.
func sanitize(s string) string {
	return strings.Map(func(c rune) rune {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			return c
		}
		return '-'
	}, s)
}

func agentCaps() []string { return agent.OramaCaps }
