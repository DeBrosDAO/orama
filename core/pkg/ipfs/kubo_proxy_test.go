package ipfs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

func TestKuboProxyMultiaddr_dialsTheAbsoluteSocket(t *testing.T) {
	m, err := ma.NewMultiaddr(KuboProxyMultiaddr)
	if err != nil {
		t.Fatal(err)
	}
	network, addr, err := manet.DialArgs(m)
	if err != nil {
		t.Fatal(err)
	}
	if network != "unix" || addr != KuboProxySocket {
		t.Fatalf("DialArgs(%s) = %s %s, want unix %s", KuboProxyMultiaddr, network, addr, KuboProxySocket)
	}
}

func TestKuboProxy_addsTheBearerAndIsPrivate(t *testing.T) {
	const token = "abc123"
	got := make(chan string, 1)
	upstream := httptestServer(t, func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})
	dir := shortDir(t)
	socket := filepath.Join(dir, "api.sock")
	ln, err := listenProxy(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %o, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %o, want 0700", dirInfo.Mode().Perm())
	}
	srv := &http.Server{Handler: kuboProxy(upstream, token)}
	go srv.Serve(ln)

	client := unixClient(socket)
	resp, err := client.Post("http://ipfs/api/v0/id", "", nil)
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
			Secret:     "secret",
			Upstream:   upstream,
			SocketPath: filepath.Join(dir, "api.sock"),
			Binary:     "/bin/sh",
			Args:       []string{"-c", "touch " + marker + "; exec sleep 30"},
			ReadyWait:  5 * time.Second,
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

func TestListenProxy_refusesToReplaceARegularFile(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "api.sock")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listenProxy(path); err == nil {
		t.Fatal("a regular file at the socket path was replaced")
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

func unixClient(socket string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
		Timeout: 2 * time.Second,
	}
}
