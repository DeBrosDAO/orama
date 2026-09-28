package ipfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// ipfs-cluster picks its HTTP transport from the multiaddr's network. For
// "unix" it uses httpunix, which ignores request cancellation and so disables
// pin_timeout; the proxy address must resolve to TCP on loopback.
func TestKuboProxyMultiaddr_isTheLoopbackTCPProxy(t *testing.T) {
	m, err := ma.NewMultiaddr(KuboProxyMultiaddr())
	if err != nil {
		t.Fatal(err)
	}
	network, addr, err := manet.DialArgs(m)
	if err != nil {
		t.Fatal(err)
	}
	if network != "tcp4" || addr != KuboProxyAddr() {
		t.Fatalf("DialArgs(%s) = %s %s, want tcp4 %s", KuboProxyMultiaddr(), network, addr, KuboProxyAddr())
	}
}

func TestKuboProxy_addsTheBearer(t *testing.T) {
	const token = "abc123"
	got := make(chan string, 1)
	upstream := httptestServer(t, func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	addr, _ := serveTestProxy(t, upstream, token, sameUID)

	resp, err := testClient().Post("http://"+addr+"/api/v0/id", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	select {
	case header := <-got:
		if header != "Bearer "+token {
			t.Errorf("upstream Authorization = %q", header)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream saw no request")
	}
}

// Bug 2722: ipfs-cluster's pin_timeout cancels a pin/add that makes no
// progress. The cancel must reach Kubo, or Kubo keeps the pin lock and repo
// gc waits behind it forever. Kubo streams progress every 500ms, so the
// stream never goes quiet on its own.
func TestKuboProxy_clientCancelReachesUpstream(t *testing.T) {
	upstreamDone := make(chan struct{})
	upstream := httptestServer(t, func(w http.ResponseWriter, r *http.Request) {
		defer close(upstreamDone)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				if _, err := fmt.Fprintln(w, `{"Progress":3}`); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	})
	addr, _ := serveTestProxy(t, upstream, "token", sameUID)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+addr+"/api/v0/pin/add?arg=Qm&progress=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, resp.Body)
		readDone <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the client's read did not end after its request was cancelled")
	}
	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the upstream request kept running after the client cancelled")
	}
}

func TestKuboProxy_refusesAnotherUsersSocket(t *testing.T) {
	var hits atomic.Int32
	upstream := httptestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	otherUID := func(_, _ netip.AddrPort) (uint32, error) { return uint32(os.Getuid()) + 1, nil }
	addr, refused := serveTestProxy(t, upstream, "token", otherUID)

	if resp, err := testClient().Post("http://"+addr+"/api/v0/id", "", nil); err == nil {
		resp.Body.Close()
		t.Fatalf("another user's connection got HTTP %d", resp.StatusCode)
	}
	if hits.Load() != 0 {
		t.Fatal("another user's request reached Kubo with the bearer")
	}
	select {
	case err := <-refused:
		if !strings.Contains(err.Error(), "belongs to uid") {
			t.Errorf("refusal %q does not name the owner", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the refusal was not reported")
	}
}

// A connection whose owner cannot be read is refused, not admitted.
func TestKuboProxy_refusesWhenTheOwnerIsUnknown(t *testing.T) {
	upstream := httptestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	unknown := func(_, _ netip.AddrPort) (uint32, error) { return 0, errSocketNotFound }
	addr, refused := serveTestProxy(t, upstream, "token", unknown)

	if resp, err := testClient().Post("http://"+addr+"/api/v0/id", "", nil); err == nil {
		resp.Body.Close()
		t.Fatalf("a connection with no known owner got HTTP %d", resp.StatusCode)
	}
	select {
	case err := <-refused:
		if !errors.Is(err, errSocketNotFound) {
			t.Errorf("refusal %v does not carry the lookup error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the refusal was not reported")
	}
}

func TestListenProxy_refusesAddressesOffLoopbackIPv4(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", "10.0.0.1:0", "[::1]:0", "localhost:0", ""} {
		if ln, err := listenProxy(addr, 0, sameUID, func(error) {}); err == nil {
			ln.Close()
			t.Errorf("listenProxy(%q) listened; the bearer proxy must be IPv4 loopback only", addr)
		}
	}
}

func TestWaitForKubo_refusesARejectedBearerImmediately(t *testing.T) {
	upstream := httptestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	start := time.Now()
	err := waitForKubo(context.Background(), upstream, "token", 30*time.Second)
	if err == nil {
		t.Fatal("a 401 was treated as ready")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("401 retried for %s; it cannot succeed later", time.Since(start))
	}
}

func TestWaitForKubo_waitsUntilTheRPCAnswers(t *testing.T) {
	var n atomic.Int32
	upstream := httptestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := waitForKubo(context.Background(), upstream, "token", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if n.Load() < 3 {
		t.Fatalf("ready on attempt %d; the first refusals must be retried", n.Load())
	}
}

func TestServeCluster_runsTheChildOnceKuboIsReady(t *testing.T) {
	token := mustToken(t, "secret")
	upstream := httptestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != kuboReadyPath || r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"ID":"ok"}`)
	})
	dir := shortDir(t)
	marker := filepath.Join(dir, "child-ran")
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeCluster(ctx, ServeClusterConfig{
			Secret:      "secret",
			Upstream:    upstream,
			ListenAddr:  "127.0.0.1:0",
			Binary:      "/bin/sh",
			Args:        []string{"-c", "touch " + marker + "; exec sleep 30"},
			ReadyWait:   5 * time.Second,
			SocketOwner: sameUID,
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(marker); statErr == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ServeCluster returned %v, want the cancel", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("child did not run: %v", statErr)
	}
}

func TestServeCluster_refusesAnEmptySecret(t *testing.T) {
	t.Setenv("CLUSTER_SECRET", "")
	err := ServeCluster(context.Background(), ServeClusterConfig{
		Binary: "/no/such/ipfs-cluster",
	})
	if err == nil {
		t.Fatal("an empty CLUSTER_SECRET started the cluster")
	}
}

func mustToken(t *testing.T, secret string) string {
	t.Helper()
	token, err := KuboAPIToken(secret)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "kp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func httptestServer(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	return ln.Addr().String()
}

func testClient() *http.Client { return &http.Client{Timeout: 2 * time.Second} }

// sameUID admits every connection as this process's own.
func sameUID(_, _ netip.AddrPort) (uint32, error) { return uint32(os.Getuid()), nil }

// serveTestProxy runs the proxy on an ephemeral loopback port and returns its
// address and the refusals it reports.
func serveTestProxy(t *testing.T, upstream, token string, owner socketOwnerFunc) (string, <-chan error) {
	t.Helper()
	refused := make(chan error, 4)
	ln, err := listenProxy("127.0.0.1:0", uint32(os.Getuid()), owner, func(err error) { refused <- err })
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: kuboProxy(upstream, token)}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String(), refused
}
