package fleet

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
)

// forwardBudget bounds opening one forward (connect and handshake).
const forwardBudget = time.Minute

// CleanupContext is a context for work inside a t.Cleanup: t.Context() is
// already cancelled when cleanups run, so a request made with it fails at
// once and the cleanup silently does nothing. It is independent of the test
// and bounded by CleanupBudget.
func CleanupContext(t testing.TB) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), CleanupBudget)
}

// ContextFor is t.Context() while the test runs and a CleanupContext once it
// has ended (inside a cleanup): helpers that may be called from either use it.
func ContextFor(t testing.TB) (context.Context, context.CancelFunc) {
	t.Helper()
	if ctx := t.Context(); ctx.Err() == nil {
		return context.WithCancel(ctx)
	}
	return CleanupContext(t)
}

// Tunnel forwards a loopback port of the runner to remoteAddr as node n
// reaches it ("127.0.0.1:31003" is the node's own loopback: the chain REST
// API, RPC, a namespace service), over SSH with the run's key and pinned
// host key, like `ssh -L`. It returns "127.0.0.1:<port>" on the runner and
// closes when the test ends.
func (f *Fleet) Tunnel(t testing.TB, n Node, remoteAddr string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), forwardBudget)
	defer cancel()
	fw, err := sshx.LocalForward(ctx, f.target(n), remoteAddr)
	f.recordForward(t, n, fmt.Sprintf("tunnel %s -> %s", addrOf(fw), remoteAddr), err)
	if err != nil {
		t.Fatalf("tunnel to %s on %s: %v", remoteAddr, n.Name, err)
	}
	t.Cleanup(func() {
		if err := fw.Close(); err != nil {
			t.Errorf("cleanup: tunnel to %s on %s: %v", remoteAddr, n.Name, err)
		}
	})
	return fw.Addr
}

// ReverseForward makes localAddr, a server this test runs (a mock APNs,
// Expo or ntfy upstream), reachable from node n: n listens on a loopback
// port of its own and every connection to it is carried back to localAddr,
// like `ssh -R`. It returns the node-side "127.0.0.1:<port>" to configure
// on the node, and closes the listener when the test ends.
func (f *Fleet) ReverseForward(t testing.TB, n Node, localAddr string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), forwardBudget)
	defer cancel()
	fw, err := sshx.RemoteForward(ctx, f.target(n), localAddr)
	f.recordForward(t, n, fmt.Sprintf("reverse forward %s (node) -> %s (runner)", addrOf(fw), localAddr), err)
	if err != nil {
		t.Fatalf("reverse forward from %s to %s: %v", n.Name, localAddr, err)
	}
	t.Cleanup(func() {
		if err := fw.Close(); err != nil {
			t.Errorf("cleanup: reverse forward from %s: %v", n.Name, err)
		}
	})
	return fw.Addr
}

func (f *Fleet) target(n Node) sshx.Target {
	return sshx.Target{Host: n.PublicIP, User: n.SSHUser, KeyFile: f.State.SSHKeyFile, KnownHostsFile: f.State.KnownHostsFile}
}

func addrOf(fw *sshx.Forward) string {
	if fw == nil {
		return "(none)"
	}
	return fw.Addr
}

// recordForward writes the opening of a forward as evidence.
func (f *Fleet) recordForward(t testing.TB, n Node, what string, opErr error) {
	t.Helper()
	rec := evidence.Record{Kind: evidence.KindSSH, Test: t.Name(), Summary: n.Name + ": " + what}
	if opErr != nil {
		rec.Error = opErr.Error()
	}
	if err := f.rec.Add(rec); err != nil {
		t.Error(errors.Join(fmt.Errorf("failed to record the forward on %s", n.Name), err))
	}
}
