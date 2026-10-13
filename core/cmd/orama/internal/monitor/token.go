package monitor

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/auth"
)

// tokenCacheTTL is how long a bearer is reused before the credential store is
// asked again. The store renews a session inside its own margin, so asking
// again this often keeps the token live without a store read per request.
const tokenCacheTTL = 30 * time.Second

// sessionEndedText is how pkg/auth says a stored session is gone for good
// (its refresh token was refused and there is no key to fall back on). It
// carries no sentinel, so the message is the only way to tell it apart from
// a network failure it may wrap.
const sessionEndedText = "session has ended"

// tokenCache hands out one bearer at a time. Renewing a session rotates its
// refresh token, so two renewals racing (a manual refresh while the stream
// reconnects) would present a retired token and end the session; the lock
// makes concurrent callers wait for the one renewal in flight.
type tokenCache struct {
	fetch func() (string, error)
	now   func() time.Time

	mu    sync.Mutex
	token string
	at    time.Time
}

func newTokenCache(fetch func() (string, error)) *tokenCache {
	return &tokenCache{fetch: fetch, now: time.Now}
}

// get returns the cached bearer while it is fresh, and fetches one otherwise.
// A failed fetch is not cached.
func (c *tokenCache) get() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Sub(c.at) < tokenCacheTTL {
		return c.token, nil
	}
	token, err := c.fetch()
	if err != nil {
		return "", err
	}
	c.token, c.at = token, c.now()
	return token, nil
}

// invalidate drops the cached bearer, after the gateway refused it.
func (c *tokenCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = ""
}

// tokenError classifies a failure to get a bearer. Only a missing credential
// or an ended session needs `orama auth login`. Failing to reach the gateway
// to renew one, or any answer that did not refuse the session — a 5xx, a rate
// limit — leaves the session intact (pkg/auth ends it only on 401/403), so
// it is an outage to retry, not a reason to sign in again.
func tokenError(env, gatewayURL string, err error) error {
	if !strings.Contains(err.Error(), sessionEndedText) {
		var urlErr *url.Error
		var gwErr *auth.GatewayError
		if errors.As(err, &urlErr) || (errors.As(err, &gwErr) && !sessionRefused(gwErr.Status)) {
			return withSSHHint(clierr.Unavailable("cannot renew the session with the %s gateway at %s: %v", env, gatewayURL, err))
		}
	}
	return clierr.Auth("no usable credentials for the %s gateway at %s: %v; sign in with `orama network use %s` then `orama auth login`",
		env, gatewayURL, err, env)
}

// sessionRefused is whether a renewal status means the gateway refused the
// session itself.
func sessionRefused(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}
