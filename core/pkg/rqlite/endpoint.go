package rqlite

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
	"gopkg.in/yaml.v3"
)

// rqliteScheme is how every rqlited is reached: plain HTTP on the WireGuard
// overlay, which provides the encryption.
const rqliteScheme = "http"

// Endpoint is how a client reaches one rqlited's HTTP API: the address that
// instance actually binds, and the basic-auth credentials its -auth requires.
//
// rqlited binds only its WireGuard advertise address and, on this release,
// always runs with -auth, so "localhost" and "no credentials" are both wrong
// for every caller, including processes on the same node. The one endpoint
// without credentials is a 0.122.x node's index rqlite, which ran without
// -auth and whose node.yaml names no credentials (preAuthConfig): the upgrade
// reads it before it upgrades the node. Every in-node client, CLI command and
// installer step builds its URL from an Endpoint instead of assembling one.
type Endpoint struct {
	Host     string
	Port     int
	Username string
	// Password never serializes: an Endpoint held in a struct that is logged
	// or encoded must not carry it out.
	Password string `json:"-" yaml:"-"`
}

// GoString keeps %#v from printing the password.
func (e Endpoint) GoString() string { return "rqlite.Endpoint(" + e.BaseURL() + ")" }

// NewEndpoint validates an rqlited address and its credentials. hostPort is
// the address the instance binds ("10.0.0.1:10100").
func NewEndpoint(hostPort, username, password string) (Endpoint, error) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(hostPort))
	if err != nil {
		return Endpoint{}, fmt.Errorf("rqlite address %q is not host:port: %w", hostPort, err)
	}
	if host == "" {
		return Endpoint{}, fmt.Errorf("rqlite address %q has no host", hostPort)
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		return Endpoint{}, fmt.Errorf("rqlite address %q is a wildcard, not an address rqlited can be reached on", hostPort)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return Endpoint{}, fmt.Errorf("rqlite address %q has an invalid port", hostPort)
	}
	if username == "" || password == "" {
		return Endpoint{}, fmt.Errorf("rqlite credentials for %s are missing — rqlited runs with -auth, "+
			"set database.rqlite_username and database.rqlite_password (secrets/rqlite-password)", hostPort)
	}
	return Endpoint{Host: host, Port: port, Username: username, Password: password}, nil
}

// IndexEndpoint is this node's index rqlited, from the node config: the host
// rqlited binds (discovery.http_adv_address, see BindAddr), the configured
// HTTP port and the configured credentials. It is the one derivation shared by
// the node process (RQLiteManager), the orama CLI and the installer, so none of
// them can disagree with where rqlited is listening.
func IndexEndpoint(db *config.DatabaseConfig, disc *config.DiscoveryConfig) (Endpoint, error) {
	if db == nil || disc == nil {
		return Endpoint{}, fmt.Errorf("index rqlite endpoint: database and discovery config are required")
	}
	addr, err := BindAddr(disc.HttpAdvAddress, db.RQLitePort)
	if err != nil {
		return Endpoint{}, fmt.Errorf("index rqlite endpoint: %w", err)
	}
	if preAuthConfig(db) {
		return unauthenticatedEndpoint(addr)
	}
	ep, err := NewEndpoint(addr, db.RQLiteUsername, db.RQLitePassword)
	if err != nil {
		return Endpoint{}, fmt.Errorf("index rqlite endpoint: %w", err)
	}
	return ep, nil
}

// preAuthConfig reports whether db is a node config written before rqlite
// authentication: no auth file and no credentials at all. 0.122.x rendered
// exactly that, and its node process started the index rqlited without -auth,
// so such a node is reached without credentials. A current node.yaml always
// names the auth file, so a current config that lost its credentials is still
// refused by NewEndpoint rather than read as a legacy one.
func preAuthConfig(db *config.DatabaseConfig) bool {
	return db.RQLiteAuthFile == "" && db.RQLiteUsername == "" && db.RQLitePassword == ""
}

// unauthenticatedEndpoint is the endpoint of an index rqlited started without
// -auth (preAuthConfig), validated like any other address.
func unauthenticatedEndpoint(hostPort string) (Endpoint, error) {
	ep, err := NewEndpoint(hostPort, preAuthPlaceholder, preAuthPlaceholder)
	if err != nil {
		return Endpoint{}, fmt.Errorf("index rqlite endpoint: %w", err)
	}
	ep.Username, ep.Password = "", ""
	return ep, nil
}

// preAuthPlaceholder satisfies NewEndpoint's credential check for an endpoint
// that has none; unauthenticatedEndpoint clears it again.
const preAuthPlaceholder = "-"

// EndpointFromNodeConfig reads the index rqlite endpoint out of a node.yaml
// (config.ProductionNodeConfigPath on a node). Used by everything that runs on
// a node without being the node process: the orama CLI and the installer.
// Those run as root and node.yaml belongs to the orama user, so it is read
// through root — the rootfs anchor it lives under — without following a
// symlink and only up to rootfs.SmallFileLimit.
//
// The decode is deliberately lenient about unknown keys: an operator command
// must keep working against a node.yaml written by the previous release in the
// middle of a rolling upgrade.
func EndpointFromNodeConfig(root rootfs.Root, path string) (Endpoint, error) {
	cfg, err := readNodeConfig(root, path)
	if err != nil {
		return Endpoint{}, err
	}
	ep, err := IndexEndpoint(&cfg.Database, &cfg.Discovery)
	if err != nil {
		return Endpoint{}, fmt.Errorf("node config %s: %w", path, err)
	}
	return ep, nil
}

// JoinAddressFromNodeConfig is the raft address the node's index rqlite joins,
// empty on the node that started the cluster. node.yaml is read as
// EndpointFromNodeConfig reads it.
func JoinAddressFromNodeConfig(root rootfs.Root, path string) (string, error) {
	cfg, err := readNodeConfig(root, path)
	if err != nil {
		return "", err
	}
	return cfg.Database.RQLiteJoinAddress, nil
}

func readNodeConfig(root rootfs.Root, path string) (*config.Config, error) {
	data, err := root.ReadFile(path, rootfs.SmallFileLimit)
	if err != nil {
		return nil, fmt.Errorf("read node config %s (run this on an installed node, as root): %w", path, err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse node config %s: %w", path, err)
	}
	return &cfg, nil
}

// LocalNodeEndpoint is the index rqlite of the node this process runs on, from
// the installed node.yaml (config.ProductionNodeConfigPath). For the orama CLI
// and other on-node tooling; the node process itself uses IndexEndpoint on its
// loaded config.
func LocalNodeEndpoint() (Endpoint, error) {
	return EndpointFromNodeConfig(rootfs.At(config.ProductionBaseDir), config.ProductionNodeConfigPath)
}

// EndpointFromDSN turns a configured rqlite DSN into an Endpoint. Credentials
// embedded in the DSN win; otherwise username/password are used. Query
// parameters are ignored.
func EndpointFromDSN(dsn, username, password string) (Endpoint, error) {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil || u.Host == "" {
		return Endpoint{}, fmt.Errorf("rqlite DSN %q is not a URL with a host", RedactDSN(dsn))
	}
	// rqlited serves plain HTTP on the WireGuard overlay (the overlay is the
	// encryption). An https DSN would be silently downgraded by every URL an
	// Endpoint builds, so it is refused rather than rewritten.
	if u.Scheme != rqliteScheme {
		return Endpoint{}, fmt.Errorf("rqlite DSN %s: scheme %q is not supported — rqlited is reached over %s on the WireGuard overlay",
			RedactDSN(dsn), u.Scheme, rqliteScheme)
	}
	if u.User != nil {
		if pass, ok := u.User.Password(); ok && u.User.Username() != "" {
			username, password = u.User.Username(), pass
		}
	}
	ep, err := NewEndpoint(u.Host, username, password)
	if err != nil {
		return Endpoint{}, fmt.Errorf("rqlite DSN %s: %w", RedactDSN(dsn), err)
	}
	return ep, nil
}

// HostPort is "host:port".
func (e Endpoint) HostPort() string {
	return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

// BaseURL is "http://host:port" without credentials. Safe to log.
func (e Endpoint) BaseURL() string {
	return rqliteScheme + "://" + e.HostPort()
}

// String is BaseURL, so formatting an Endpoint never prints the password.
func (e Endpoint) String() string {
	return e.BaseURL()
}

// CredentialedURL is "http://user:pass@host:port", for consumers that take a
// DSN (gateway YAML, the CoreDNS plugin). Never log it; use RedactDSN.
func (e Endpoint) CredentialedURL() string {
	return (&url.URL{Scheme: rqliteScheme, User: url.UserPassword(e.Username, e.Password), Host: e.HostPort()}).String()
}

// SQLDSN is the gorqlite database/sql DSN for this endpoint at read
// consistency level.
func (e Endpoint) SQLDSN(level ReadConsistency) string {
	return buildRQLiteDSNWithLevel(e.Host, e.Port, e.Username, e.Password, string(level))
}

// Admin is an AdminClient for this endpoint.
func (e Endpoint) Admin() *AdminClient {
	return NewAdminClient(e.BaseURL(), e.Username, e.Password)
}
