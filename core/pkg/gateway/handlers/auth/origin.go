package auth

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// requestOrigin returns the domain and URI to name inside a sign-in message.
//
// The domain is the whole point of the format: it is what makes a signature
// collected by one site useless at another, so it has to be the host the user's
// client actually resolved and connected to, not a name this gateway holds an
// opinion about.
//
// A public request reaches the gateway from Caddy on 127.0.0.1. Caddy passes
// the original Host header through untouched and sets X-Forwarded-Proto itself,
// overwriting anything the client sent — it trusts no incoming X-Forwarded-*
// header without a trusted_proxies list, and none is configured. So r.Host is
// the public host and X-Forwarded-Proto is the real scheme.
//
// A request that did not come through Caddy carries whatever Host its caller
// chose. That is not a hole: the check this feeds compares the message's domain
// to this same host, so a caller who controls both is signing a message for a
// name they picked, with their own wallet, and gains nothing by it. The nonce
// row is what decides whether the challenge was ever issued.
func requestOrigin(r *http.Request) (domain, uri string, err error) {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return "", "", errors.New("request carries no Host header, so a sign-in message has no domain to name")
	}

	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	u := url.URL{Scheme: scheme, Host: host}
	return hostWithoutPort(host), u.String(), nil
}

// origin is the domain and URI a sign-in message on this gateway names.
//
// A namespace gateway is never reached directly. The cluster gateway proxies
// ns-<namespace>.<base domain> to it over WireGuard with Host rewritten to the
// upstream's address, so r.Host there is 10.0.0.x:port, a name no client
// connected to, and every client refused the message (stagenet, 2026-09-29).
// Its public host is fixed by its namespace and base domain, so it names that,
// from its own configuration rather than from a header anyone could set.
func (h *Handlers) origin(r *http.Request) (domain, uri string, err error) {
	if h.publicHost != "" {
		return h.publicHost, "https://" + h.publicHost, nil
	}
	return requestOrigin(r)
}

// signsInTo reports whether a sign-in to namespace belongs on this gateway. A
// namespace gateway's messages name its own host, so one for another namespace
// would read "ns-a wants you to sign in to b".
func (h *Handlers) signsInTo(namespace string) bool {
	return h.publicHost == "" || namespace == h.defaultNS
}

// wrongNamespace is the refusal for a sign-in to another namespace on a
// namespace gateway.
func (h *Handlers) wrongNamespace(namespace string) string {
	return "this gateway, " + h.publicHost + ", signs in to namespace " + h.defaultNS +
		" only; sign in to " + namespace + " at its own gateway"
}

// hostWithoutPort strips a port from an authority. An IPv6 literal is
// bracketed, so only a colon after the closing bracket is a port separator.
func hostWithoutPort(host string) string {
	i := strings.LastIndex(host, ":")
	if i < 0 || strings.Contains(host[i:], "]") {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(host[:i], "[]")
}
