package namespace

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

// tenantRQLiteURL is the base URL of a namespace rqlited on the node with
// WireGuard IP host. rqlited binds exactly that address (its advertise host,
// see rqlite.BindAddr), so there is no default: an empty host yields a URL the
// spawner refuses, rather than a guessed loopback nothing listens on.
//
// It carries no credentials; the spawner adds them where it writes the
// consumer's config (withRQLiteCredentials).
func tenantRQLiteURL(host string, port int) string {
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// withRQLiteCredentials validates an rqlite DSN and returns it with the
// cluster-wide credentials attached. Used by the spawner for every config it
// writes that talks to rqlite (gateway, SFU).
func withRQLiteCredentials(dsn, user, pass string) (string, error) {
	// The host's credentials win over any already in the DSN: a gateway YAML
	// read back from disk carries the ones it was written with, and after a
	// password change those are stale.
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil {
		return "", fmt.Errorf("rqlite DSN %s is not a URL: %w", rqlite.RedactDSN(dsn), err)
	}
	u.User = nil
	ep, err := rqlite.EndpointFromDSN(u.String(), user, pass)
	if err != nil {
		return "", err
	}
	return ep.CredentialedURL(), nil
}

// tenantRQLiteEndpoint is the namespace rqlited on the node with WireGuard IP
// host, with the cluster-wide credentials, for the cluster manager's own
// probes and admin calls.
func (cm *ClusterManager) tenantRQLiteEndpoint(host string, port int) (rqlite.Endpoint, error) {
	user, pass, err := cm.systemdSpawner.readRQLitePassword()
	if err != nil {
		return rqlite.Endpoint{}, err
	}
	addr, err := rqlite.BindAddr(net.JoinHostPort(host, strconv.Itoa(port)), port)
	if err != nil {
		return rqlite.Endpoint{}, fmt.Errorf("namespace rqlite on %q: %w", host, err)
	}
	return rqlite.NewEndpoint(addr, user, pass)
}
