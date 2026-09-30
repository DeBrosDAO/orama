package gateway

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/routepolicy"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// A wallet's grant may be narrowed to a resource — `cache:key=sessions/*`,
// `storage:avatars/*` — and the data path applies the narrowing by reading the
// grant's selector. On a route that does not require ownership no grant is
// resolved, so the wallet was handed the whole data plane and the selector was
// never read: a grant narrowed to sessions/* could write tokens/x.
//
// The grant is resolved for those routes now, and it is resolved from a short
// cache, because reading it is registry round trips (~300ms) and cache and
// storage are the hot path. What the cache costs is bounded by its lifetime: a
// grant narrowed or revoked reaches the data plane within narrowedGrantTTL, on
// every node, and a grant already narrowed never widens in the meantime.
const (
	narrowedGrantTTL = 10 * time.Second

	// narrowedGrantCacheMax bounds the cache. A full cache drops its expired
	// entries, then one arbitrary live one: it used to be emptied, so a caller
	// cycling through wallets could flush every other caller's entry and send
	// each of their requests to the registry.
	narrowedGrantCacheMax = 4096
)

// grantCache remembers the grant each caller held in a namespace, including
// that it held none. The zero value is ready to use.
type grantCache struct {
	mu      sync.Mutex
	entries map[string]grantCacheEntry
}

type grantCacheEntry struct {
	grant   *auth.Grant
	expires time.Time
}

func (c *grantCache) get(key string, now time.Time) (*auth.Grant, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !now.Before(e.expires) {
		return nil, false
	}
	return e.grant, true
}

func (c *grantCache) put(key string, grant *auth.Grant, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]grantCacheEntry)
	}
	if _, present := c.entries[key]; !present && len(c.entries) >= narrowedGrantCacheMax {
		c.makeRoom(now)
	}
	c.entries[key] = grantCacheEntry{grant: grant, expires: now.Add(narrowedGrantTTL)}
}

// makeRoom drops the expired entries, or one arbitrary entry when none has
// expired. The caller holds mu.
func (c *grantCache) makeRoom(now time.Time) {
	for k, e := range c.entries {
		if !now.Before(e.expires) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) < narrowedGrantCacheMax {
		return
	}
	for k := range c.entries {
		delete(c.entries, k)
		return
	}
}

// grantIsNarrowable reports whether a selector on a grant can change what this
// route allows: the route sits in a domain the data path narrows, and does not
// already resolve the grant itself through the ownership gate.
func grantIsNarrowable(policy routepolicy.Policy) bool {
	return !policy.Ownership && auth.SelectorEnforced(auth.SelectorDomain(policy.Domain))
}

// callerHoldsNarrowedGrant reports whether this request's wallet holds a grant
// narrowed to a resource in the route's domain. An error means the grant could
// not be read, and the request is refused rather than given the data plane.
//
// Only a wallet's: a key's scopes are its authority on a route that does not
// resolve a grant, and they are read from the row on every request already.
func (g *Gateway) callerHoldsNarrowedGrant(r *http.Request, policy routepolicy.Policy) (bool, error) {
	if !grantIsNarrowable(policy) {
		return false, nil
	}
	claims, _ := r.Context().Value(ctxKeyJWT).(*auth.JWTClaims)
	if claims == nil || strings.TrimSpace(claims.Sub) == "" || auth.IsAPIKeySubject(claims.Sub) {
		return false, nil
	}
	grant, err := g.cachedRequestGrant(r, claims.Sub)
	if err != nil {
		return false, err
	}
	return grant != nil && strings.TrimSpace(grant.Resource) != "", nil
}

// cachedRequestGrant is lookupRequestGrant through the cache. Only an answer
// is cached, "no grant" included; a failed read is not, so it cannot stand in
// for the grant for the cache's lifetime.
func (g *Gateway) cachedRequestGrant(r *http.Request, subject string) (*auth.Grant, error) {
	key := g.requestNamespace(r) + "\x00" + strings.TrimSpace(subject)
	now := time.Now()
	if grant, ok := g.narrowedGrants.get(key, now); ok {
		return grant, nil
	}
	grant, err := g.lookupRequestGrant(r)
	if err != nil {
		return nil, err
	}
	g.narrowedGrants.put(key, grant, now)
	return grant, nil
}

// resolveRequestGrant is the grant a route that does not require ownership
// still has to carry: the cached one where a selector may apply, the live one
// for a control route, where a stale answer would be a stale refusal.
func (g *Gateway) resolveRequestGrant(r *http.Request, policy routepolicy.Policy) (*auth.Grant, error) {
	if !grantIsNarrowable(policy) {
		return g.lookupRequestGrant(r)
	}
	claims, _ := r.Context().Value(ctxKeyJWT).(*auth.JWTClaims)
	if claims == nil {
		return nil, nil
	}
	return g.cachedRequestGrant(r, claims.Sub)
}

// refuseUnreadableGrant answers a request whose grant could not be read: a
// retryable 503, never the data plane a missing grant would leave.
func (g *Gateway) refuseUnreadableGrant(w http.ResponseWriter, err error) {
	g.logger.ComponentError(logging.ComponentGeneral, "could not read the caller's grant; refusing the request", zap.Error(err))
	writeError(w, http.StatusServiceUnavailable, "the caller's grant could not be read right now; retry shortly")
}
