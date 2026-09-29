package fleet

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
	"github.com/DeBrosOfficial/network/e2e/harness/sshx/sshxtest"
)

// sshFleet is a fleet whose one node is an in-process sshd.
func sshFleet(t *testing.T) (*Fleet, Node, string) {
	t.Helper()
	srv := sshxtest.Start(t)
	dir := t.TempDir()
	rec, err := evidence.New(dir, "fleet", nil)
	if err != nil {
		t.Fatal(err)
	}
	n := Node{Name: "node-1", PublicIP: srv.Addr, SSHUser: srv.User}
	st := &State{RunID: "abcd1234", Nodes: []Node{n}, SSHKeyFile: srv.KeyFile, KnownHostsFile: srv.KnownHostsFile}
	return New(st, rec), n, filepath.Join(dir, "fleet.jsonl")
}

func TestTunnel_reachesNodeServiceAndClosesAtCleanup(t *testing.T) {
	f, n, evFile := sshFleet(t)
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "chain api")
	}))
	defer svc.Close()
	var addr string
	tb := runFailTB(t, func(tb testing.TB) {
		addr = f.Tunnel(tb, n, strings.TrimPrefix(svc.URL, "http://"))
		resp, err := http.Get("http://" + addr + "/cosmos/base/tendermint/v1beta1/node_info")
		if err != nil {
			tb.Fatal(err)
		}
		defer resp.Body.Close()
		if body, _ := io.ReadAll(resp.Body); string(body) != "chain api" {
			tb.Fatalf("body %q", body)
		}
	})
	if tb.Failed() {
		t.Fatal(tb.msgs)
	}
	if c, err := net.Dial("tcp", addr); err == nil {
		c.Close()
		t.Fatal("the tunnel is still listening after the test's cleanup")
	}
	raw, _ := os.ReadFile(evFile)
	if !strings.Contains(string(raw), "node-1: tunnel 127.0.0.1:") {
		t.Fatalf("no evidence of the tunnel: %s", raw)
	}
}

func TestReverseForward_nodeReachesMockProvider(t *testing.T) {
	f, n, _ := sshFleet(t)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "apns ok")
	}))
	defer mock.Close()
	tb := runFailTB(t, func(tb testing.TB) {
		nodeAddr := f.ReverseForward(tb, n, strings.TrimPrefix(mock.URL, "http://"))
		out := f.MustExec(tb, n, "curl -s http://"+nodeAddr+"/3/device/x")
		if out.Stdout != "apns ok" {
			tb.Fatalf("from the node: %q", out.Stdout)
		}
	})
	if tb.Failed() {
		t.Fatal(tb.msgs)
	}
}

func TestTunnel_unpinnedNodeFailsTheTest(t *testing.T) {
	f, n, _ := sshFleet(t)
	f.State.KnownHostsFile = filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(f.State.KnownHostsFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tb := runFailTB(t, func(tb testing.TB) { f.Tunnel(tb, n, "127.0.0.1:1") })
	if !tb.Failed() || !strings.Contains(strings.Join(tb.msgs, " "), "tunnel to 127.0.0.1:1 on node-1") {
		t.Fatalf("msgs %v", tb.msgs)
	}
}

func TestCleanupContext_liveAfterTestContextEnds(t *testing.T) {
	var inCleanup context.Context
	t.Run("sub", func(t *testing.T) {
		t.Cleanup(func() {
			if t.Context().Err() == nil {
				t.Error("t.Context() is still live in a cleanup: the pitfall this guards against is gone")
			}
			ctx, cancel := ContextFor(t)
			defer cancel()
			inCleanup = ctx
			if ctx.Err() != nil {
				t.Error("ContextFor in a cleanup is already done")
			}
		})
		ctx, cancel := ContextFor(t)
		defer cancel()
		if ctx.Err() != nil {
			t.Fatal("ContextFor during the test is done")
		}
	})
	if inCleanup == nil {
		t.Fatal("the cleanup did not run")
	}
	cctx, cancel := CleanupContext(t)
	defer cancel()
	if _, ok := cctx.Deadline(); !ok {
		t.Fatal("CleanupContext has no bound")
	}
}
