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
	mc.apiKeyNSMu.Lock()
	defer mc.apiKeyNSMu.Unlock()

	mc.apiKeyNS[apiKey] = &cachedValue{
		value:     namespace,
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
