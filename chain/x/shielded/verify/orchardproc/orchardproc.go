// Package orchardproc is the second verifier of x/shielded (C12a): the same upstream orchard
// 0.15.5 checks, run by a separately built and separately pinned Rust binary
// (chain/x/shielded/orchardverifier-bin) that the node keeps running out of process.
//
// What the split gives: a memory-corruption bug, a link problem or a hang in one verifier cannot
// silently pass a bundle in the other. What it does not give: independence of the verification
// logic, because both binaries call the same upstream crate. See docs/CHAIN.md.
//
// Protocol (every frame length-prefixed, u32 big-endian):
//
//	request  = u64 BE id || sighash (32) || bundle
//	response = u64 BE id || result code (1)
//	ready    = a response with id ReadyID and code 0, sent once after the verifying key is built
//
// The process is started on first use and kept. Requests are serialized. A request that times
// out, or whose process dies or answers out of protocol, kills the process and is rejected with
// verify.ErrVerifierFault; the next request starts a fresh process. Fail closed: a fault is
// never turned into an accept and a request is never retried.
package orchardproc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/chain/x/shielded/bundle"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify"
	"github.com/DeBrosOfficial/network/chain/x/shielded/verify/orchard"
)

const (
	// ReadyID is the id of the ready response.
	ReadyID = ^uint64(0)

	// DefaultRequestTimeout is the hard limit for one verification, from the first byte written
	// to the last byte read. Verifying takes milliseconds; this is a hang detector, generous
	// enough that a loaded node does not trip it.
	DefaultRequestTimeout = 30 * time.Second
	// DefaultStartTimeout bounds the start-up: the process builds the Halo 2 verifying key
	// before it says it is ready.
	DefaultStartTimeout = 60 * time.Second

	// The result codes, equal to the in-process verifier's.
	codeOK                = 0
	codeMalformed         = 1
	codeBadProofLength    = 2
	codeProofRejected     = 3
	codeSignatureRejected = 4
	codePanic             = 5

	idLen       = 8
	responseLen = idLen + 1
)

// Config configures a Verifier.
type Config struct {
	// Path is the verifier binary.
	Path string
	// ChainID is the chain the sighash is bound to.
	ChainID string
	// SHA256 is the hex SHA-256 the binary must have. The verifier refuses to start a file with
	// another hash, and refuses to start at all without a pin: two nodes must not disagree about a
	// bundle because one has a different binary.
	SHA256 string
	// RequestTimeout and StartTimeout default to the package constants when zero.
	RequestTimeout time.Duration
	StartTimeout   time.Duration
}

// Verifier is a verify.Verifier backed by the out-of-process binary. It is safe for concurrent
// use; requests are serialized.
type Verifier struct {
	cfg Config

	mu     sync.Mutex
	proc   *process
	nextID uint64
}

var _ verify.Verifier = (*Verifier)(nil)

// VerifierID is this verifier's identity for verify.Check's distinctness rule. It differs from the
// in-process library's, so the two count as two verifiers.
const VerifierID = "orchard-process"

// ID implements verify.Verifier.
func (*Verifier) ID() string { return VerifierID }

// New returns a Verifier. It does not start the process; the first Verify (or Warm) does.
func New(cfg Config) (*Verifier, error) {
	if cfg.ChainID == "" {
		return nil, verify.ErrEmptyChainID
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	return &Verifier{cfg: cfg}, nil
}

// Warm starts the process and waits for its ready frame, so the verifying key is built before the
// first bundle. A missing binary, a bad pin or a process that will not start is an error.
func (v *Verifier) Warm() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, err := v.running()
	return err
}

// Close stops the process. The Verifier can still be used; it starts a new one.
func (v *Verifier) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.stop()
	return nil
}

// Verify accepts a bundle only when the binary says every check passed.
func (v *Verifier) Verify(b, binding []byte) error {
	if len(b) == 0 || len(b) > bundle.MaxBytes {
		return fmt.Errorf("%w: %d bytes", verify.ErrMalformed, len(b))
	}
	sighash, err := orchard.Sighash(v.cfg.ChainID, binding, b)
	if err != nil {
		return err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	p, err := v.running()
	if err != nil {
		return err
	}
	v.nextID++
	id := v.nextID
	code, err := p.exchange(id, sighash[:], b, v.cfg.RequestTimeout)
	if err != nil {
		v.stop()
		return fmt.Errorf("%w: out-of-process verifier: %w", verify.ErrVerifierFault, err)
	}
	return codeToError(code)
}

func codeToError(code byte) error {
	switch code {
	case codeOK:
		return nil
	case codeMalformed:
		return verify.ErrMalformed
	case codeBadProofLength:
		return verify.ErrProofLength
	case codeProofRejected:
		return verify.ErrProofRejected
	case codeSignatureRejected:
		return verify.ErrSignatureRejected
	case codePanic:
		return fmt.Errorf("%w: the out-of-process verifier panicked", verify.ErrVerifierFault)
	default:
		return fmt.Errorf("%w: out-of-process verifier result code %d", verify.ErrVerifierFault, code)
	}
}

// running returns the live process, starting one when there is none or the last one exited.
func (v *Verifier) running() (*process, error) {
	if v.proc != nil && !v.proc.exited() {
		return v.proc, nil
	}
	v.stop()
	p, err := start(v.cfg)
	if err != nil {
		return nil, err
	}
	v.proc = p
	return p, nil
}

func (v *Verifier) stop() {
	if v.proc != nil {
		v.proc.kill()
		v.proc = nil
	}
}

type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	done   chan struct{}
}

func start(cfg Config) (*process, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("%w: no out-of-process verifier binary configured (flag --shielded-verifier)", verify.ErrVerifierNotLinked)
	}
	if err := checkPin(cfg); err != nil {
		return nil, err
	}
	cmd := exec.Command(cfg.Path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: verifier stdin: %w", verify.ErrVerifierFault, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: verifier stdout: %w", verify.ErrVerifierFault, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: start %q: %w", verify.ErrVerifierFault, cfg.Path, err)
	}
	p := &process{cmd: cmd, stdin: stdin, stdout: stdout, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	id, code, err := p.roundTrip(nil, cfg.StartTimeout)
	if err != nil || id != ReadyID || code != codeOK {
		p.kill()
		if err == nil {
			err = fmt.Errorf("ready frame has id %d code %d", id, code)
		}
		return nil, fmt.Errorf("%w: verifier did not become ready: %w", verify.ErrVerifierFault, err)
	}
	return p, nil
}

// ErrBinaryPin means the verifier binary is not the one the node is pinned to.
var ErrBinaryPin = fmt.Errorf("%w: the verifier binary does not match its pin", verify.ErrVerifierNotLinked)

// checkPin reads the binary and compares its SHA-256 with the pin. There is a window between the
// check and the exec; the binary's directory must not be writable by anyone but the node's owner.
func checkPin(cfg Config) error {
	raw, err := os.ReadFile(cfg.Path)
	if err != nil {
		return fmt.Errorf("%w: out-of-process verifier binary %q: %w", verify.ErrVerifierNotLinked, cfg.Path, err)
	}
	if cfg.SHA256 == "" {
		return fmt.Errorf("%w: no sha256 pin is configured for %q (--shielded-verifier-sha256)", ErrBinaryPin, cfg.Path)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, cfg.SHA256) {
		return fmt.Errorf("%w: %q has sha256 %s, the pin is %s", ErrBinaryPin, cfg.Path, got, cfg.SHA256)
	}
	return nil
}

func (p *process) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *process) kill() {
	_ = p.cmd.Process.Kill()
	_ = p.stdin.Close()
	<-p.done
	_ = p.stdout.Close()
}

// exchange sends one request and returns the result code. The caller kills the process on error.
func (p *process) exchange(id uint64, sighash, b []byte, timeout time.Duration) (byte, error) {
	frame := make([]byte, 0, 4+idLen+len(sighash)+len(b))
	frame = binary.BigEndian.AppendUint32(frame, uint32(idLen+len(sighash)+len(b)))
	frame = binary.BigEndian.AppendUint64(frame, id)
	frame = append(frame, sighash...)
	frame = append(frame, b...)
	gotID, code, err := p.roundTrip(frame, timeout)
	if err != nil {
		return 0, err
	}
	if gotID != id {
		return 0, fmt.Errorf("answered request %d, want %d", gotID, id)
	}
	return code, nil
}

type reply struct {
	id   uint64
	code byte
	err  error
}

// roundTrip writes frame (nothing when nil) and reads one response frame within timeout. The
// I/O runs in a goroutine so a hung process cannot hold the caller; on timeout the caller kills
// the process, which closes the pipes and ends the goroutine.
func (p *process) roundTrip(frame []byte, timeout time.Duration) (uint64, byte, error) {
	out := make(chan reply, 1)
	go func() {
		if frame != nil {
			if _, err := p.stdin.Write(frame); err != nil {
				out <- reply{err: fmt.Errorf("write request: %w", err)}
				return
			}
		}
		out <- readResponse(p.stdout)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-out:
		return r.id, r.code, r.err
	case <-timer.C:
		return 0, 0, fmt.Errorf("no answer within %s", timeout)
	}
}

func readResponse(r io.Reader) reply {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return reply{err: fmt.Errorf("read response length: %w", err)}
	}
	if n := binary.BigEndian.Uint32(head[:]); n != responseLen {
		return reply{err: errors.New("response frame has the wrong length")}
	}
	var body [responseLen]byte
	if _, err := io.ReadFull(r, body[:]); err != nil {
		return reply{err: fmt.Errorf("read response: %w", err)}
	}
	return reply{id: binary.BigEndian.Uint64(body[:idLen]), code: body[idLen]}
}
