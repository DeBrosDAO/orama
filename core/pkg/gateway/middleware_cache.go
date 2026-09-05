package gateway

import (
	"sync"
	"time"
)

// middlewareCache provides in-memory TTL caching for frequently-queried middleware
// data that rarely changes. This eliminates redundant RQLite round-trips for:
//   - API key → namespace lookups (authMiddleware, validateAuthForNamespaceProxy)
//   - Namespace → gateway targets (handleNamespaceGatewayRequest)
type middlewareCache struct {
	// apiKeyToNamespace caches API key → namespace name mappings.
	// These rarely change and are looked up on every authenticated request.
	apiKeyNS   map[string]*cachedValue
	apiKeyNSMu sync.RWMutex

	// nsGatewayTargets caches namespace → []gatewayTarget for namespace routing.
	// Updated infrequently (only when namespace clusters change).
	nsTargets   map[string]*cachedGatewayTargets
	nsTargetsMu sync.RWMutex

	ttl    time.Duration
	stopCh chan struct{}
}

type cachedValue struct {
	value     string
	scopes    string // api_keys.scopes (bugboard #148); "" means grandfather=admin
	expiresAt time.Time
}

type gatewayTarget struct {
	ip   string
	port int
}

type cachedGatewayTargets struct {
	targets   []gatewayTarget
	expiresAt time.Time
}

func newMiddlewareCache(ttl time.Duration) *middlewareCache {
	mc := &middlewareCache{
		apiKeyNS:  make(map[string]*cachedValue),
		nsTargets: make(map[string]*cachedGatewayTargets),
		ttl:       ttl,
		stopCh:    make(chan struct{}),
	}
	go mc.cleanup()
	return mc
}

// Stop stops the background cleanup goroutine.
func (mc *middlewareCache) Stop() {
	close(mc.stopCh)
}

// GetAPIKeyNamespace returns the cached namespace for an API key, or "" if not cached/expired.
func (mc *middlewareCache) GetAPIKeyNamespace(apiKey string) (string, bool) {
	mc.apiKeyNSMu.RLock()
	defer mc.apiKeyNSMu.RUnlock()

	entry, ok := mc.apiKeyNS[apiKey]
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.value, true
}

// SetAPIKeyNamespace caches an API key → namespace mapping.
func (mc *middlewareCache) SetAPIKeyNamespace(apiKey, namespace string) {
	mc.SetAPIKeyEntry(apiKey, namespace, "")
}

// GetAPIKeyEntry returns the cached namespace AND scopes for an API key
// (bugboard #148). scopes is the raw api_keys.scopes value ("" = grandfather).
func (mc *middlewareCache) GetAPIKeyEntry(apiKey string) (namespace, scopes string, ok bool) {
	mc.apiKeyNSMu.RLock()
	defer mc.apiKeyNSMu.RUnlock()

	entry, present := mc.apiKeyNS[apiKey]
	if !present || time.Now().After(entry.expiresAt) {
		return "", "", false
	}
	return entry.value, entry.scopes, true
}

// SetAPIKeyEntry caches an API key → (namespace, permissions) mapping.
//
// The TTL is CredentialStaleness, and it is the one number that describes how
// long a change to what a key may do takes to bite. **Revoking** a key is not
// bounded by it: lookupAPIKeyEntry consults the replicated revocation list
// before the cache, so a revoked key stops at once, everywhere. **Narrowing**
// one — editing its scopes, or revoking a grant it holds — is, because the
// permissions come off the cached row.
func (mc *middlewareCache) SetAPIKeyEntry(apiKey, namespace, scopes string) {
	mc.apiKeyNSMu.Lock()
	defer mc.apiKeyNSMu.Unlock()

	mc.apiKeyNS[apiKey] = &cachedValue{
		value:     namespace,
		scopes:    scopes,
		expiresAt: time.Now().Add(mc.ttl),
	}
}

// GetNamespaceTargets returns cached gateway targets for a namespace, or nil if not cached/expired.
func (mc *middlewareCache) GetNamespaceTargets(namespace string) ([]gatewayTarget, bool) {
	mc.nsTargetsMu.RLock()
	defer mc.nsTargetsMu.RUnlock()

	entry, ok := mc.nsTargets[namespace]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.targets, true
}

// SetNamespaceTargets caches namespace gateway targets.
func (mc *middlewareCache) SetNamespaceTargets(namespace string, targets []gatewayTarget) {
	mc.nsTargetsMu.Lock()
	defer mc.nsTargetsMu.Unlock()

	mc.nsTargets[namespace] = &cachedGatewayTargets{
		targets:   targets,
		expiresAt: time.Now().Add(mc.ttl),
	}
}

// cleanup periodically removes expired entries to prevent memory leaks.
func (mc *middlewareCache) cleanup() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()

			mc.apiKeyNSMu.Lock()
			for k, v := range mc.apiKeyNS {
				if now.After(v.expiresAt) {
					delete(mc.apiKeyNS, k)
				}
			}
			mc.apiKeyNSMu.Unlock()

			mc.nsTargetsMu.Lock()
			for k, v := range mc.nsTargets {
				if now.After(v.expiresAt) {
					delete(mc.nsTargets, k)
				}
			}
			mc.nsTargetsMu.Unlock()
		case <-mc.stopCh:
			return
		}
	}
}

// CredentialStaleness is how long a change to what a credential may do takes to
// take effect.
//
// It is the middleware cache's TTL, and it is named because it is a promise
// rather than a tuning knob: an operator who narrows a key, or revokes a grant,
// is entitled to know when that lands. One minute, on every gateway that had
// seen the credential.
//
// Two things are deliberately not bounded by it. **Revocation** is checked
// against the replicated revocation list before the cache is consulted, so a
// revoked key or token stops at once. And a **wallet's** grant is resolved per
// request by the authorization middleware, so narrowing one is immediate.
const CredentialStaleness = 60 * time.Second
