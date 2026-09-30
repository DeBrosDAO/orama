package oramacli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// readChunk is how much stdout one read takes.
const readChunk = 4096

// Proc is a running orama invocation (Start): its stdout arrives line by line
// while it runs; Wait collects the Result and records it as evidence, with
// the same isolation, pacing and redaction as Run.
type Proc struct {
	Args []string

	r   *Runner
	cmd *exec.Cmd
	// stdoutR is the read end of the CLI's stdout pipe.
	stdoutR *os.File
	cancel  context.CancelFunc
	start   time.Time
	plan    *pacePlan
	stderr  bytes.Buffer
	// input is the stdin given as bytes, recorded with the invocation.
	input string

	mu      sync.Mutex
	stdout  bytes.Buffer
	pending []string
	eof     bool
	readErr error
	notify  chan struct{}

	lines    chan string
	stop     chan struct{}
	readDone chan struct{}

	waitOnce sync.Once
	res      Result
	err      error
}

// Start runs orama with args in the background, after the same checks and
// pacing as Run. The process ends when it exits, when Kill is called, when
// ctx ends or when DefaultBudget passes. Every Proc must be waited for.
func (r *Runner) Start(ctx context.Context, args ...string) (*Proc, error) {
	return r.StartWith(ctx, RunOpts{}, args...)
}

// StartWith is Start with a working directory, extra environment and stdin
// (opts, checked as RunWith checks them). opts.Stdin is written and
// recorded; opts.StdinReader streams, for answering prompts as they come.
func (r *Runner) StartWith(ctx context.Context, opts RunOpts, args ...string) (*Proc, error) {
	if err := errors.Join(r.Check(), r.checkOpts(opts)); err != nil {
		return nil, err
	}
	plan, err := r.paceBefore(ctx, args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultBudget)
	p := &Proc{Args: args, r: r, cmd: r.command(ctx, opts, args), cancel: cancel, plan: plan, input: string(opts.Stdin),
		notify: make(chan struct{}, 1), lines: make(chan string), stop: make(chan struct{}), readDone: make(chan struct{})}
	p.cmd.Stdin = opts.stdin()
	p.cmd.Stderr = &p.stderr
	err = p.startPiped()
	if err != nil {
		cancel()
		err = fmt.Errorf("failed to start orama %s: %w", strings.Join(RedactArgs(args), " "), err)
		return nil, errors.Join(err, r.record(Result{Args: args, Exit: -1}, p.input, err))
	}
	go p.read(p.stdoutR)
	go p.forward()
	return p, nil
}

// startPiped starts the command with stdout on a pipe of our own: Wait
// returns when the CLI exits, and the pipe's reader sees EOF once every
// process holding its write end (the CLI and anything it started) closed it.
func (p *Proc) startPiped() error {
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create orama's stdout pipe: %w", err)
	}
	p.cmd.Stdout = w
	p.start = time.Now()
	err = p.cmd.Start()
	if cerr := w.Close(); cerr != nil {
		err = errors.Join(err, fmt.Errorf("failed to close our end of orama's stdout pipe: %w", cerr))
	}
	if err != nil {
		return errors.Join(err, r.Close())
	}
	p.stdoutR = r
	return nil
}

// StdoutLines delivers stdout line by line (without the newline). It is
// closed after the last line once the process has closed its stdout, or when
// Wait returns; lines not received by then are in Result.Stdout.
func (p *Proc) StdoutLines() <-chan string { return p.lines }

// Kill ends the process and every process it started (its process group);
// Wait still has to be called. Killing a process that has already exited is
// not an error.
func (p *Proc) Kill() error {
	if err := killGroup(p.cmd.Process); err != nil {
		return fmt.Errorf("failed to kill orama %s: %w", strings.Join(RedactArgs(p.Args), " "), err)
	}
	return nil
}

// Wait waits for the process to exit and returns its Result, like Run: a
// non-zero exit is Result.Exit, err means it could not run or be recorded.
// It records the evidence once; later calls return the same values.
func (p *Proc) Wait() (Result, error) {
	p.waitOnce.Do(p.collect)
	return p.res, p.err
}

func (p *Proc) collect() {
	waitErr := p.cmd.Wait()
	var stray error
	if errors.Is(waitErr, exec.ErrWaitDelay) {
		stray = strayOutput(p.cmd, p.Args)
	}
	stray = errors.Join(stray, p.drainStdout())
	close(p.stop)
	p.cancel()
	p.mu.Lock()
	res := Result{Args: p.Args, Stdout: p.stdout.String(), Stderr: p.stderr.String(), Duration: time.Since(p.start)}
	readErr := p.readErr
	p.mu.Unlock()
	res, err := finish(res, waitErr)
	err = errors.Join(err, stray, readErr, p.plan.after())
	if recErr := p.r.record(res, p.input, err); recErr != nil {
		err = errors.Join(err, recErr)
	}
	p.res, p.err = res, err
}

// drainStdout waits for stdout to end after the CLI exited. A process it
// left holding stdout gets cliWaitDelay; then its group is killed and the
// pipe closed, so Wait never hangs on it.
func (p *Proc) drainStdout() error {
	timer := time.NewTimer(cliWaitDelay)
	defer timer.Stop()
	select {
	case <-p.readDone:
		return p.stdoutR.Close()
	case <-timer.C:
	}
	err := errors.Join(strayOutput(p.cmd, p.Args), p.stdoutR.Close())
	<-p.readDone
	return err
}

// read copies stdout into the buffer, charges device-login polls, and queues
// complete lines for forward.
func (p *Proc) read(out io.Reader) {
	defer close(p.readDone)
	buf := make([]byte, readChunk)
	var partial string
	for {
		n, err := out.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			obsErr := p.plan.observe(chunk)
			var lines []string
			lines, partial = splitLines(partial + string(chunk))
			p.enqueue(chunk, lines, obsErr, false)
		}
		if err != nil {
			var readErr error
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				readErr = fmt.Errorf("failed to read orama's stdout: %w", err)
			}
			var last []string
			if partial != "" {
				last = []string{partial}
			}
			p.enqueue(nil, last, readErr, true)
			return
		}
	}
}

func (p *Proc) enqueue(chunk []byte, lines []string, err error, eof bool) {
	p.mu.Lock()
	p.stdout.Write(chunk)
	p.pending = append(p.pending, lines...)
	p.readErr = errors.Join(p.readErr, err)
	p.eof = p.eof || eof
	p.mu.Unlock()
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

// forward hands queued lines to StdoutLines without ever blocking read, so a
// test that stops reading cannot stall the CLI.
func (p *Proc) forward() {
	defer close(p.lines)
	for {
		p.mu.Lock()
		if len(p.pending) > 0 {
			line := p.pending[0]
			p.pending = p.pending[1:]
			p.mu.Unlock()
			select {
			case p.lines <- line:
				continue
			case <-p.stop:
				return
			}
		}
		eof := p.eof
		p.mu.Unlock()
		if eof {
			return
		}
		select {
		case <-p.notify:
		case <-p.stop:
			return
		}
	}
}

// splitLines splits text into complete lines and the unterminated rest.
func splitLines(text string) ([]string, string) {
	parts := strings.Split(text, "\n")
	lines := parts[:len(parts)-1]
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines, parts[len(parts)-1]
}
