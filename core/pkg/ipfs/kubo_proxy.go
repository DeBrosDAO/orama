package ipfs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

const (
	// KuboProxySocket is the socket ipfs-cluster dials. ipfs-cluster v1.1.2
	// cannot send Kubo's bearer — its ipfshttp connector sets only
	// Content-Type, and service.json has no header field — so the cluster
	// unit listens here and forwards to the RPC with the header. The runtime
	// directory is 0700 and the socket is 0600, both owned by the orama user,
	// so a DynamicUser deployment cannot open it.
	KuboProxySocket = "/run/orama-ipfs/api.sock"

	// KuboProxyMultiaddr is KuboProxySocket in the form ipfs-cluster stores.
	// /unix/run/... dials the absolute path /run/... (manet.DialArgs).
	KuboProxyMultiaddr = "/unix/run/orama-ipfs/api.sock"

	kuboReadyPath = "/api/v0/id"
)

// ServeClusterConfig is how the cluster unit reaches Kubo. Zero values are
// the production ones: CLUSTER_SECRET from the environment, the proxy socket,
// Kubo's loopback RPC, and ipfs-cluster-service daemon.
type ServeClusterConfig struct {
	Secret     string
	Upstream   string
	SocketPath string
	Binary     string
	Args       []string
	ReadyWait  time.Duration
}

func (c *ServeClusterConfig) fill() error {
	if c.Secret == "" {
		c.Secret = os.Getenv("CLUSTER_SECRET")
	}
	if c.Secret == "" {
		return errors.New("CLUSTER_SECRET is empty; the cluster unit's environment file carries it")
	}
	if c.Upstream == "" {
		c.Upstream = net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", constants.IPFSAPIPort))
	}
	if c.SocketPath == "" {
		c.SocketPath = KuboProxySocket
	}
	if c.Binary == "" {
		c.Binary = "/usr/local/bin/ipfs-cluster-service"
	}
	if len(c.Args) == 0 {
		c.Args = []string{"daemon"}
	}
	if c.ReadyWait == 0 {
		c.ReadyWait = 30 * time.Second
	}
	return nil
}

// ServeCluster waits until Kubo accepts the bearer, listens on the proxy
// socket, and runs ipfs-cluster as a child. It returns when the child exits
// or ctx is cancelled. Cancelling sends the child SIGTERM.
func ServeCluster(ctx context.Context, cfg ServeClusterConfig) error {
	if err := cfg.fill(); err != nil {
		return err
	}
	token, err := KuboAPIToken(cfg.Secret)
	if err != nil {
		return err
	}
	if err := waitForKubo(ctx, cfg.Upstream, token, cfg.ReadyWait); err != nil {
		return err
	}
	ln, err := listenProxy(cfg.SocketPath)
	if err != nil {
		return err
	}
	defer ln.Close()
	srv := &http.Server{Handler: kuboProxy(cfg.Upstream, token)}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()

	cmd := exec.Command(cfg.Binary, cfg.Args...)
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cfg.Binary, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		<-done
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("ipfs-cluster exited: %w", err)
		}
		return errors.New("ipfs-cluster exited")
	}
}

// waitForKubo polls Kubo's id RPC. A 401 is the bearer being wrong, which
// retrying cannot fix. Connection refusal is Kubo still starting.
func waitForKubo(ctx context.Context, upstream, token string, wait time.Duration) error {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTimer(0)
	defer tick.Stop()
	rawURL := "http://" + upstream + kuboReadyPath
	var last error
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("IPFS API not ready after %s: %w", wait, last)
		case <-tick.C:
		}
		reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		resp, err := PostAPI(reqCtx, rawURL, token)
		cancel()
		if err == nil {
			resp.Body.Close()
			switch resp.StatusCode {
			case http.StatusOK:
				return nil
			case http.StatusUnauthorized:
				return fmt.Errorf("kubo refused the bearer (401); it is derived from CLUSTER_SECRET and retrying will not change it")
			default:
				last = fmt.Errorf("GET %s: HTTP %d", kuboReadyPath, resp.StatusCode)
			}
		} else {
			last = err
		}
		tick.Reset(time.Second)
	}
}

// listenProxy binds socketPath at 0600, replacing a leftover socket. The
// directory is 0700. Anything else already at the path is an error.
func listenProxy(socketPath string) (net.Listener, error) {
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("restrict %s: %w", dir, err)
	}
	if fi, err := os.Lstat(socketPath); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			return nil, fmt.Errorf("remove leftover socket %s: %w", socketPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat %s: %w", socketPath, err)
	}
	old := syscall.Umask(0o077)
	ln, err := net.Listen("unix", socketPath)
	syscall.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("restrict %s: %w", socketPath, err)
	}
	return ln, nil
}

// kuboProxy forwards to upstream and sets Kubo's bearer on every request,
// replacing whatever the caller sent. The socket's mode is the access check.
func kuboProxy(upstream, token string) http.Handler {
	target := &url.URL{Scheme: "http", Host: upstream}
	proxy := httputil.NewSingleHostReverseProxy(target)
	base := proxy.Director
	proxy.Director = func(r *http.Request) {
		base(r)
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return proxy
}
