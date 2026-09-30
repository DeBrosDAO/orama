package ipfs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

const kuboReadyPath = "/api/v0/id"

// ipfs-cluster v1.1.6 cannot send Kubo's bearer — its ipfshttp connector sets
// only Content-Type, and service.json has no header field — so the cluster
// unit listens on loopback and forwards to the RPC with the header.
//
// The listener is TCP, not a unix socket. For a /unix node_multiaddress the
// connector uses github.com/tv42/httpunix, whose RoundTrip ignores the
// request context: when pin_timeout (or unpin_timeout, ipfs_request_timeout)
// cancels a request, nothing closes the connection, the connector stays
// blocked reading the response, and Kubo keeps the request — and a pin/add's
// pin lock — forever, which starves repo gc. For a /ip4 address the connector
// uses net/http's own transport, which closes the connection on cancel; the
// proxy then cancels the upstream request and Kubo aborts it.
//
// Access is checked per connection instead of by file mode: see
// ownerOnlyListener.

// KuboProxyAddr is the proxy's listen address.
// kuboProxyHeaderTimeout bounds how long a client may take to send a
// request's headers; the body of a pin or add streams for as long as it needs.
const kuboProxyHeaderTimeout = 10 * time.Second

func KuboProxyAddr() string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(constants.IPFSClusterKuboProxyPort))
}

// KuboProxyMultiaddr is KuboProxyAddr in the form ipfs-cluster stores in
// service.json (ipfs_connector.ipfshttp.node_multiaddress).
func KuboProxyMultiaddr() string {
	return fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", constants.IPFSClusterKuboProxyPort)
}

// ServeClusterConfig is how the cluster unit reaches Kubo. Zero values are
// the production ones: CLUSTER_SECRET from the environment, the proxy
// address, Kubo's loopback RPC, ipfs-cluster-service daemon, and connections
// admitted when the kernel (sock_diag) says they belong to this process's uid.
type ServeClusterConfig struct {
	Secret      string
	Upstream    string
	ListenAddr  string
	Binary      string
	Args        []string
	ReadyWait   time.Duration
	SocketOwner socketOwnerFunc
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
	if c.ListenAddr == "" {
		c.ListenAddr = KuboProxyAddr()
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
	if c.SocketOwner == nil {
		c.SocketOwner = sockDiagOwner
	}
	return nil
}

// ServeCluster waits until Kubo accepts the bearer, listens on the proxy
// address, and runs ipfs-cluster as a child. It returns when the child exits
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
	ln, err := listenProxy(cfg.ListenAddr, uint32(os.Getuid()), cfg.SocketOwner, reportRefused)
	if err != nil {
		return err
	}
	defer ln.Close()
	srv := &http.Server{Handler: kuboProxy(cfg.Upstream, token), ReadHeaderTimeout: kuboProxyHeaderTimeout}
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

// listenProxy listens on addr, which must be an IPv4 loopback address, and
// admits only connections dialled by a socket owned by uid.
func listenProxy(addr string, uid uint32, owner socketOwnerFunc, refuse func(error)) (net.Listener, error) {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return nil, fmt.Errorf("parse proxy address %q: %w", addr, err)
	}
	if !ap.Addr().Is4() || !ap.Addr().IsLoopback() {
		return nil, fmt.Errorf("proxy address %s is not IPv4 loopback; the proxy adds Kubo's bearer and must not be reachable off the node", addr)
	}
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	return &ownerOnlyListener{Listener: ln, uid: uid, owner: owner, refuse: refuse}, nil
}

// reportRefused writes a refused connection to the unit's journal.
func reportRefused(err error) {
	fmt.Fprintf(os.Stderr, "serve-ipfs-cluster: kubo proxy %v\n", err)
}

// kuboProxy forwards to upstream and sets Kubo's bearer on every request,
// replacing whatever the caller sent. The listener is the access check. The
// inbound request's context is the upstream request's: when the connector
// cancels and closes its connection, the upstream request is cancelled too.
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
