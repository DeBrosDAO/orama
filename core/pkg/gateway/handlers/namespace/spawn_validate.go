package namespace

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// A spawn request is authenticated — a MAC under the cluster secret, over the
// overlay — but what it carries still becomes paths and a shell command line
// on this node. The namespace names directories, one of which a fresh rqlite
// start removes recursively; the node ID names the rqlite data directory; the
// join addresses are substituted into `sh -c '… ${JOIN_ARGS} …'`, and the
// join verify URL receives the cluster's rqlite credentials. None of them
// was checked, so any holder of the cluster secret — any node — could run a
// command as orama here or delete a directory of its choosing. They are
// checked where they arrive.

// nodeIDPattern is a node's identity: a libp2p peer ID in practice. It has to
// be a single path component that is not "." or "..".
var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// validate checks the fields of req that become names, paths or command-line
// text on this node.
func (req *SpawnRequest) validate() error {
	if req.Namespace != strings.TrimSpace(req.Namespace) || !httputil.ValidateNamespace(req.Namespace) {
		return fmt.Errorf("namespace %q is not valid: letters, digits, '-' and '_', starting with a letter or digit, at most 64", req.Namespace)
	}
	if !nodeIDPattern.MatchString(req.NodeID) {
		return fmt.Errorf("node_id %q is not valid: letters, digits, '.', '-' and '_', starting with a letter or digit", req.NodeID)
	}
	if req.Action != "spawn-rqlite" {
		return nil
	}
	for field, addr := range map[string]string{
		"rqlite_http_adv_addr": req.RQLiteHTTPAdvAddr,
		"rqlite_raft_adv_addr": req.RQLiteRaftAdvAddr,
	} {
		if err := validateIPPort(addr); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	for i, addr := range req.RQLiteJoinAddrs {
		if err := validateIPPort(addr); err != nil {
			return fmt.Errorf("rqlite_join_addrs[%d]: %w", i, err)
		}
	}
	if req.RQLiteJoinVerifyURL != "" {
		if err := validateJoinVerifyURL(req.RQLiteJoinVerifyURL, req.RQLiteJoinAddrs); err != nil {
			return fmt.Errorf("rqlite_join_verify_url: %w", err)
		}
	}
	return nil
}

// validateJoinVerifyURL accepts http://<IP>:<port> and nothing more, on the
// host of one of the join addresses: the node about to be joined, whose HTTP
// port the cluster manager names (the join address carries its Raft port).
// The spawner sends the cluster-wide rqlite credentials to this URL, so it
// must not be free to name another host, a path or a query.
func validateJoinVerifyURL(raw string, joinAddrs []string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%q is not a URL: %w", raw, err)
	}
	if u.Scheme != "http" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("%q is not http://<ip>:<port>", raw)
	}
	if err := validateIPPort(u.Host); err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(u.Host)
	for _, addr := range joinAddrs {
		joinHost, _, _ := net.SplitHostPort(addr)
		if net.ParseIP(joinHost).Equal(net.ParseIP(host)) {
			return nil
		}
	}
	return fmt.Errorf("%q is not on the host of any join address %v", raw, joinAddrs)
}

// validateIPPort accepts an IP literal and a port, e.g. 10.0.0.2:10001. Every
// rqlite address the cluster manager sends is a node's WireGuard IP and a port
// from its allocation; a hostname is not one of them.
func validateIPPort(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q is not host:port: %w", addr, err)
	}
	if net.ParseIP(host) == nil {
		return fmt.Errorf("%q: host %q is not an IP address", addr, host)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
		return fmt.Errorf("%q: port %q is not a port number", addr, port)
	}
	return nil
}
