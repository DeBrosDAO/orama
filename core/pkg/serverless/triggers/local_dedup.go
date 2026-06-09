package triggers

import (
	"sync"
	"time"
)

// Bugboard #555 — messages:new trigger fires twice (duplicate push).
//
// Two distinct bugs produced duplicate dispatches:
//
//  1. Cross-node fail-open: claimDispatch (dispatcher.go) coordinates
//     once-per-publish dispatch via Olric, but FAILS OPEN when Olric is
//     unavailable/misconfigured. On a multi-node cluster every node that
//     receives the gossip publish then fires the handler → N duplicate
//     invocations (AnChat: exactly 2 on a 2-reachable-node cluster).
//
//  2. Single-node self-delivery: even on one node, gossipsub can deliver a
//     locally-originated publish back to the same node's subscribe handler,
//     and the only guard was the cross-node Olric claim — which is a no-op
//     when Olric is down.
//
// localDedupCache fixes (2) and bounds the blast radius of (1): a single
// node never invokes the SAME publish twice, regardless of Olric health.
// It is a small bounded map with per-entry TTL, keyed by the SAME string
// dispatchDedupKey produces — (namespace, topic, sha256(payload)[:16]).
//
// IDENTICAL-PAYLOAD CAVEAT: the key folds the payload hash, NOT a stable
// message id (gossipsub's message-ID isn't plumbed through the subscribe
// handler, and parsing an app-specific id would couple the dispatcher to a
// tenant's JSON schema). So two byte-identical publishes within the TTL
// window collapse to one local invocation. Real payloads carry a unique id
// (messageId/seq), so this is not a practical concern; it is the same
// trade-off documented on dispatchDedupKey.
const (
	// localDedupTTL bounds how long a (namespace, topic, payload) claim is
	// remembered on this node. It must cover gossipsub self-delivery /
	// fan-out jitter without de-duplicating legitimately-repeated publishes
	// seconds apart. Kept in lockstep with dispatchDedupTTL.
	localDedupTTL = 30 * time.Second

	// localDedupMaxEntries caps the cache so a high-throughput namespace
	// can't grow it without bound. When the cap is hit, expired entries are
	// swept first; if still full, the claim is allowed through (fail-open —
	// a rare duplicate is far better than dropping a wake).
	localDedupMaxEntries = 4096
)

// localDedupCache is a bounded, TTL'd set of recently-dispatched keys for a
// single node. Safe for concurrent use.
type localDedupCache struct {
	mu      sync.Mutex
	entries map[string]time.Time // key -> expiry
	ttl     time.Duration
	maxSize int
	now     func() time.Time // injectable clock for tests
}

// newLocalDedupCache builds a cache with the package default TTL and size.
func newLocalDedupCache() *localDedupCache {
	return &localDedupCache{
		entries: make(map[string]time.Time),
		ttl:     localDedupTTL,
		maxSize: localDedupMaxEntries,
		now:     time.Now,
	}
}

// claim records the key and reports whether THIS node may dispatch it now.
//
// Returns true the first time a key is seen within the TTL window (caller
// should dispatch) and false on subsequent calls within the window (caller
// should skip — it's a local duplicate).
//
// Fail-open: if the cache is at capacity and can't be swept enough to make
// room, claim returns true (allow dispatch) rather than risk dropping a
// legitimate wake.
func (c *localDedupCache) claim(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if exp, ok := c.entries[key]; ok && now.Before(exp) {
		return false // seen recently → local duplicate → skip
	}

	// Either unseen or the previous entry expired. Sweep expired entries
	// before inserting so the map doesn't accumulate dead keys.
	if len(c.entries) >= c.maxSize {
		c.sweepExpiredLocked(now)
	}
	if len(c.entries) >= c.maxSize {
		// Still full of live entries — allow dispatch rather than drop.
		return true
	}

	c.entries[key] = now.Add(c.ttl)
	return true
}

// sweepExpiredLocked removes expired entries. Caller must hold c.mu.
func (c *localDedupCache) sweepExpiredLocked(now time.Time) {
	for k, exp := range c.entries {
		if !now.Before(exp) {
			delete(c.entries, k)
		}
	}
}
