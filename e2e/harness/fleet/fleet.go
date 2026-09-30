package fleet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
)

// Output is what one remote command produced.
type Output struct {
	Stdout   string
	Stderr   string
	Exit     int
	Duration time.Duration
}

// Shell reaches one node. The production Shell is SSH through sshx with the
// run's key and pinned host keys; unit tests substitute a fake.
type Shell interface {
	// Run executes cmd. err means it could not run; a non-zero exit is not an error.
	Run(ctx context.Context, cmd string) (Output, error)
	Put(ctx context.Context, path string, data []byte, mode os.FileMode) error
	Get(ctx context.Context, path string) ([]byte, error)
}

// Dialer returns the Shell for a node.
type Dialer func(State, Node) Shell

// Fleet is the run's state plus the helpers tests use to reach and disturb
// nodes. Every mutation a helper makes registers a t.Cleanup that undoes it.
//
// State is never written after load: parallel tests read it without a lock.
// Extras created during the package (harness.ExtraNode) are kept apart, in a
// list guarded by mu, and every node lookup reads both.
type Fleet struct {
	State *State
	dial  Dialer
	rec   *evidence.Recorder

	mu     sync.RWMutex
	extras []Node
}

// New wraps a loaded state with SSH access through sshx.
func New(st *State, rec *evidence.Recorder) *Fleet {
	return NewWithDialer(st, rec, dialSSH)
}

// NewWithDialer wraps a state with a custom dialer (unit tests).
func NewWithDialer(st *State, rec *evidence.Recorder, dial Dialer) *Fleet {
	return &Fleet{State: st, dial: dial, rec: rec}
}

// Recorder is the evidence recorder the fleet writes to (nil outside a run).
func (f *Fleet) Recorder() *evidence.Recorder { return f.rec }

// AllNodes returns the core nodes then the extras (the state's, then those
// added during this package). Probes are not cluster members and are reached
// through Probes.
func (f *Fleet) AllNodes() []Node {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append(append(append([]Node{}, f.State.Nodes...), f.State.Extras...), f.extras...)
}

// lists returns every node list, under the read lock, as copies.
func (f *Fleet) lists() [][]Node {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return [][]Node{f.State.Nodes, f.State.Extras, append([]Node{}, f.extras...), f.State.Probes}
}

// Lookup finds a node, extra or probe by name, public IP or WireGuard IP.
func (f *Fleet) Lookup(key string) (Node, bool) {
	for _, list := range f.lists() {
		for _, n := range list {
			if key != "" && (n.Name == key || n.PublicIP == key || n.WGIP == key) {
				return n, true
			}
		}
	}
	return Node{}, false
}

// Node is Lookup that fails the test when the node does not exist.
func (f *Fleet) Node(t testing.TB, key string) Node {
	t.Helper()
	n, ok := f.Lookup(key)
	if !ok {
		t.Fatalf("no node %q in run %s (have %s)", key, f.State.RunID, strings.Join(f.names(), ", "))
	}
	return n
}

func (f *Fleet) names() []string {
	var out []string
	for _, list := range f.lists() {
		for _, n := range list {
			out = append(out, n.Name)
		}
	}
	return out
}

// SSH returns a Shell for node whose commands and file transfers are
// recorded as evidence, attributed to no test (the collector's view).
func (f *Fleet) SSH(_ context.Context, n Node) Shell {
	return f.shellFor("", n)
}

// SSHFor is SSH with the evidence attributed to t.
func (f *Fleet) SSHFor(t testing.TB, n Node) Shell {
	return f.shellFor(t.Name(), n)
}

// Redact masks the run's secrets and every recognised credential in s, for
// text a test puts in a failure message.
func (f *Fleet) Redact(s string) string {
	return f.rec.Redactor().Redact(s)
}

func (f *Fleet) shellFor(test string, n Node) Shell {
	return &recordedShell{inner: f.dial(*f.State, n), node: n, test: test, rec: f.rec}
}

// recordedShell writes every command it runs to the evidence file.
type recordedShell struct {
	inner Shell
	node  Node
	test  string
	rec   *evidence.Recorder
}

func (s *recordedShell) Run(ctx context.Context, cmd string) (Output, error) {
	start := time.Now()
	out, err := s.inner.Run(ctx, cmd)
	out.Duration = time.Since(start)
	rec := evidence.Record{
		Kind: evidence.KindSSH, Test: s.test, Summary: s.node.Name + ": " + cmd,
		Status: out.Exit, DurationMS: out.Duration.Milliseconds(),
		Output: out.Stdout + stderrSuffix(out.Stderr),
	}
	return out, s.record(rec, err)
}

func (s *recordedShell) Put(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	start := time.Now()
	err := s.inner.Put(ctx, path, data, mode)
	rec := evidence.Record{
		Kind: evidence.KindSSH, Test: s.test, Summary: fmt.Sprintf("%s: put %s (%d bytes, mode %o)", s.node.Name, path, len(data), mode),
		DurationMS: time.Since(start).Milliseconds(), Input: string(data),
	}
	return s.record(rec, err)
}

func (s *recordedShell) Get(ctx context.Context, path string) ([]byte, error) {
	start := time.Now()
	data, err := s.inner.Get(ctx, path)
	rec := evidence.Record{
		Kind: evidence.KindSSH, Test: s.test, Summary: fmt.Sprintf("%s: get %s (%d bytes)", s.node.Name, path, len(data)),
		DurationMS: time.Since(start).Milliseconds(), Output: string(data),
	}
	return data, s.record(rec, err)
}

// record writes rec with opErr's text and returns opErr joined with any
// failure to record: neither hides the other.
func (s *recordedShell) record(rec evidence.Record, opErr error) error {
	if opErr != nil {
		rec.Error = opErr.Error()
	}
	if recErr := s.rec.Add(rec); recErr != nil {
		return errors.Join(opErr, fmt.Errorf("failed to record evidence for %s: %w", s.node.Name, recErr))
	}
	return opErr
}

func stderrSuffix(stderr string) string {
	if stderr == "" {
		return ""
	}
	return "\n[stderr]\n" + stderr
}

// sshShell is the production Shell.
type sshShell struct{ target sshx.Target }

func dialSSH(st State, n Node) Shell {
	return &sshShell{target: sshx.Target{Host: n.PublicIP, User: n.SSHUser, KeyFile: st.SSHKeyFile, KnownHostsFile: st.KnownHostsFile}}
}

func (s *sshShell) Run(ctx context.Context, cmd string) (Output, error) {
	stdout, stderr, exit, err := sshx.Run(ctx, s.target, cmd)
	return Output{Stdout: stdout, Stderr: stderr, Exit: exit}, err
}

func (s *sshShell) Put(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	return sshx.Put(ctx, s.target, path, data, mode)
}

func (s *sshShell) Get(ctx context.Context, path string) ([]byte, error) {
	return sshx.Get(ctx, s.target, path)
}
