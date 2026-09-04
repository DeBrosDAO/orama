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

// hostWithoutPort strips a port from an authority. An IPv6 literal is
// bracketed, so only a colon after the closing bracket is a port separator.
func hostWithoutPort(host string) string {
	i := strings.LastIndex(host, ":")
	if i < 0 || strings.Contains(host[i:], "]") {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(host[:i], "[]")
}
