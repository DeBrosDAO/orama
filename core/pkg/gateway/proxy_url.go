package gateway

import "net/url"

// proxyTargetURL is the URL a proxied request is sent to: base (scheme and
// host) plus the request's own path and query, in the form they arrived in.
//
// The path is re-escaped, not pasted in decoded. r.URL.Path has had its escapes
// resolved, so a NUL (%00) or a newline in it made http.NewRequest refuse the
// URL — the caller got a bare 500 instead of the 400 the route's validation
// gives — and an encoded "?" or "#" became the start of a query or fragment,
// moving the rest of the path into a place the route never looked.
func proxyTargetURL(base string, u *url.URL) string {
	target := base + u.EscapedPath()
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return target
}
