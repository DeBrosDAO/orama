package rqlite

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// BindAddr is the host:port rqlited listens on: the host of its advertise
// address (the WireGuard IP) with the given port.
//
// rqlited never binds a wildcard or a guessed loopback. Admin calls, joins and
// leader-forwarded writes all use the advertised address, and every client on
// the node reaches it there (see Endpoint), so an advertise address without a
// usable host is a configuration error rather than something to paper over.
func BindAddr(adv string, port int) (string, error) {
	host, err := BindHost(adv)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// BindHost is the host rqlited listens on for advertise address adv
// ("10.0.0.4:10100" or a bare "10.0.0.4"). An empty, wildcard or unparseable
// address is an error: there is no host that rqlited and its clients could
// both agree on.
func BindHost(adv string) (string, error) {
	adv = strings.TrimSpace(adv)
	if adv == "" {
		return "", fmt.Errorf("rqlite advertise address is empty — set discovery.http_adv_address (index) or the instance's HTTPAdvAddress to this node's WireGuard IP:port")
	}
	host := adv
	if h, _, err := net.SplitHostPort(adv); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return "", fmt.Errorf("rqlite advertise address %q has no host", adv)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() {
			return "", fmt.Errorf("rqlite advertise address %q is a wildcard — rqlited must bind this node's WireGuard IP", adv)
		}
		return ip.String(), nil
	}
	if strings.Contains(host, ":") {
		return "", fmt.Errorf("rqlite advertise address %q is not host:port", adv)
	}
	return host, nil
}
