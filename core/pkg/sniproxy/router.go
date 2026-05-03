package sniproxy

import (
	"strings"
	"sync"
)

// Backend describes where to forward a connection.
type Backend struct {
	// Name is for logs/metrics only. Optional.
	Name string
	// Network is the dial network ("tcp", "tcp4", "tcp6"). Default "tcp".
	Network string
	// Addr is the dial target ("127.0.0.1:5349").
	Addr string
}

// Route maps an SNI value (or wildcard pattern) to a Backend.
//
// Match semantics:
//   - "example.com"  matches exactly "example.com"
//   - "*.example.com" matches any single-label subdomain ("a.example.com"
//     but not "a.b.example.com" — single-label like DNS wildcards)
type Route struct {
	Match   string
	Backend Backend
}

// Router atomically swaps a routing table while concurrent reads are in
// flight. Reads are lock-free after the slice is published.
type Router struct {
	mu       sync.RWMutex
	routes   []Route
	fallback Backend
}

// NewRouter creates a router with no routes and the given fallback.
func NewRouter(fallback Backend) *Router {
	return &Router{fallback: fallback}
}

// Pick returns the matching backend for an SNI value, or the fallback if
// no route matches (or if sni is empty).
func (r *Router) Pick(sni string) Backend {
	if sni == "" {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.fallback
	}
	sni = strings.ToLower(sni)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, route := range r.routes {
		if matchSNI(route.Match, sni) {
			return route.Backend
		}
	}
	return r.fallback
}

// Replace atomically swaps the routing table. The new routes replace the
// old ones in their entirety; partial updates are not supported.
func (r *Router) Replace(routes []Route, fallback Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = routes
	r.fallback = fallback
}

// Routes returns a defensive copy of the current routes. For introspection.
func (r *Router) Routes() []Route {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Route, len(r.routes))
	copy(out, r.routes)
	return out
}

// Fallback returns the current fallback backend.
func (r *Router) Fallback() Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fallback
}

// matchSNI implements the Match semantics documented on Route.
func matchSNI(pattern, sni string) bool {
	pattern = strings.ToLower(pattern)
	if pattern == sni {
		return true
	}
	// "*.example.com" matches "<single-label>.example.com".
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		if !strings.HasSuffix(sni, suffix) {
			return false
		}
		labelEnd := len(sni) - len(suffix)
		if labelEnd <= 0 {
			return false
		}
		// No additional dots in the wildcard label.
		return !strings.Contains(sni[:labelEnd], ".")
	}
	return false
}
