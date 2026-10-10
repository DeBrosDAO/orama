package gw

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// PinTo returns a copy of c that dials ip (one node's public address) for
// every request, WebSocket and raw exchange, while the URL, the TLS server
// name (SNI) and the Host header stay the gateway's hostname and the CA stays
// pinned. Evidence names the pinned address. An address that does not parse
// makes every request of the copy fail with that error.
func (c *Client) PinTo(ip string) *Client {
	cp := *c
	addr := net.ParseIP(ip)
	if addr == nil {
		cp.pinErr = fmt.Errorf("gw.PinTo(%q): not an IP address", ip)
		return &cp
	}
	tr, ok := c.HTTP.Transport.(*http.Transport)
	if !ok {
		cp.pinErr = fmt.Errorf("gw.PinTo(%s): the client's transport is %T, not *http.Transport", ip, c.HTTP.Transport)
		return &cp
	}
	nt := tr.Clone()
	// A clone has a connection pool of its own: a connection opened for the
	// unpinned client never serves the pinned one.
	nt.DialContext = pinnedDial(addr.String())
	cp.HTTP = &http.Client{Transport: nt, CheckRedirect: c.HTTP.CheckRedirect, Timeout: c.HTTP.Timeout, Jar: c.HTTP.Jar}
	cp.pinIP, cp.pinErr = addr.String(), nil
	return &cp
}

// PinnedIP is the address PinTo pinned c to, or "".
func (c *Client) PinnedIP() string { return c.pinIP }

// pinnedDial dials ip on the port of the address asked for.
func pinnedDial(ip string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("failed to split %q for the pinned dial: %w", addr, err)
		}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip, port))
		if err != nil {
			return nil, fmt.Errorf("failed to dial pinned node %s for %s: %w", ip, addr, err)
		}
		return conn, nil
	}
}

// dialTarget is where a raw exchange for hostPort connects: the pinned
// address on the same port when c is pinned.
func (c *Client) dialTarget(hostPort string) (string, error) {
	if c.pinIP == "" {
		return hostPort, nil
	}
	_, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", fmt.Errorf("failed to split %q for the pinned dial: %w", hostPort, err)
	}
	return net.JoinHostPort(c.pinIP, port), nil
}

// pinNote is appended to evidence summaries of a pinned client.
func (c *Client) pinNote() string {
	if c.pinIP == "" {
		return ""
	}
	return " (pinned to " + c.pinIP + ")"
}
