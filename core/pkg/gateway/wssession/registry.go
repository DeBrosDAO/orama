// Package wssession holds an open WebSocket to the credential that opened it.
//
// A WebSocket is authorized once, at the upgrade. Everything after that used to
// be on trust: a socket opened with a fifteen-minute token kept serving for as
// long as the client kept it open, and revoking the token — or signing the
// session out — reached every new request and no open socket.
//
// Every socket opened with a token is registered here with that token's
// claims, and a sweeper re-asks the two questions the upgrade asked, on a
// timer, for as long as the socket is open: has the token expired, and has it
// been revoked. A socket that fails either is closed with a code that says
// which.
package wssession

import (
	"context"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"go.uber.org/zap"
)

const (
	// CloseExpired is the close code for a socket whose token expired without
	// being refreshed. Private-use range (4000-4999); "reconnect with a fresh
	// token".
	CloseExpired = 4401

	// CloseRevoked is the close code for a socket whose token, session or
	// subject was revoked. Reconnecting with the same credential will be
	// refused; the client has to sign in again.
	CloseRevoked = 4403

	// ReasonExpired and ReasonRevoked are the close reasons that go with the
	// two codes.
	ReasonExpired = "token expired; reconnect with a fresh token"
	ReasonRevoked = "session revoked; sign in again"

	// ExpiryGrace is how long past its token's exp a socket is kept open. It
	// covers clock skew between the gateway that minted the token and this one,
	// and the round trip of a client refreshing the token on the open socket.
	ExpiryGrace = 120 * time.Second

	// SweepInterval is how often every open socket is re-checked. The list is
	// reloaded at the start of every sweep, so a revocation recorded anywhere
	// in the cluster closes the sockets it covers within this interval.
	SweepInterval = auth.RevocationRefreshInterval

	// CloseFrameTimeout bounds the close frame sent to a socket being ended.
	// The connection is closed after it whether or not the client read it.
	CloseFrameTimeout = time.Second

	// sweepCloseConcurrency is how many sockets one sweep closes at a time. A
	// close can take up to CloseFrameTimeout against a client that stopped
	// reading, so closing one at a time would let a few such clients hold up
	// every other revocation on the gateway.
	sweepCloseConcurrency = 64
)

// Denier answers whether a token, verified when the socket opened, has been
// revoked since. *auth.Service is the one the gateway uses.
type Denier interface {
	// RefreshRevocations reloads the revocation list, so the sweep that
	// follows applies every revocation recorded before it.
	RefreshRevocations(ctx context.Context)
	Revoked(claims *auth.JWTClaims) bool
}

// Closer ends one socket: a close frame carrying code and reason, then the
// connection. It must be safe to call from a goroutine other than the one
// reading the socket.
type Closer func(code int, reason string)

// Registry is every token-authorized socket open on this gateway.
type Registry struct {
	mu      sync.Mutex
	next    uint64
	sockets map[uint64]*Socket
	logger  *zap.Logger
}

// NewRegistry builds an empty registry. logger records each socket the
// sweeper closes.
func NewRegistry(logger *zap.Logger) *Registry {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Registry{sockets: map[uint64]*Socket{}, logger: logger}
}

// Register starts holding a socket to claims. A request with no token claims —
// an API key, or no credential on a public function — has no token to expire
// or revoke, and Register returns nil; every Socket method is safe on nil.
//
// The claims are copied: the caller's may be request-scoped.
func (r *Registry) Register(claims *auth.JWTClaims, closeFn Closer) *Socket {
	if claims == nil || closeFn == nil {
		return nil
	}
	s := &Socket{registry: r, claims: *claims, close: closeFn}

	r.mu.Lock()
	r.next++
	s.id = r.next
	r.sockets[s.id] = s
	r.mu.Unlock()
	return s
}

// Len is how many sockets are registered.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sockets)
}

// Sweep closes every socket whose token expired more than ExpiryGrace ago or
// has been revoked, and reports how many it closed.
//
// The registry lock is held only to copy the set: the revocation check reads
// a shared list, and a close writes to the network. Closes run in parallel,
// up to sweepCloseConcurrency at once, and the sweep waits for them.
func (r *Registry) Sweep(now time.Time, d Denier) int {
	r.mu.Lock()
	open := make([]*Socket, 0, len(r.sockets))
	for _, s := range r.sockets {
		open = append(open, s)
	}
	r.mu.Unlock()

	var (
		wg     sync.WaitGroup
		slots  = make(chan struct{}, sweepCloseConcurrency)
		mu     sync.Mutex
		closed int
	)
	for _, s := range open {
		claims := s.Claims()
		code, reason := CloseExpired, ReasonExpired
		switch {
		case expired(claims.Exp, now):
		case d.Revoked(&claims):
			code, reason = CloseRevoked, ReasonRevoked
		default:
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func(s *Socket, claims auth.JWTClaims, code int, reason string) {
			defer wg.Done()
			defer func() { <-slots }()
			if !s.end(code, reason) {
				return
			}
			mu.Lock()
			closed++
			mu.Unlock()
			r.logger.Info("closed a WebSocket whose token no longer authorizes it",
				zap.Int("code", code),
				zap.String("subject", auth.RedactSubject(claims.Sub)),
				zap.String("jti", claims.Jti),
				zap.Int64("exp", claims.Exp))
		}(s, claims, code, reason)
	}
	wg.Wait()
	return closed
}

// Run reloads the revocation list and sweeps, every interval, until ctx is
// done.
func (r *Registry) Run(ctx context.Context, d Denier, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			// An idle gateway holds nothing to check, and reloading the list
			// for it would be a registry query every interval on every
			// gateway in the cluster for nothing.
			if r.Len() == 0 {
				continue
			}
			// Bounded by the interval: a registry that does not answer must
			// not stop expired sockets being closed on this pass.
			refreshCtx, cancel := context.WithTimeout(ctx, interval)
			d.RefreshRevocations(refreshCtx)
			cancel()
			r.Sweep(now, d)
		}
	}
}

func (r *Registry) remove(id uint64) {
	r.mu.Lock()
	delete(r.sockets, id)
	r.mu.Unlock()
}

// expired reports whether a token expiring at exp (unix seconds) is past its
// grace at now. A token with no exp has nothing to enforce: the gateway never
// mints one, and a hop that could not say gets MaxTokenLifetime instead.
func expired(exp int64, now time.Time) bool {
	if exp <= 0 {
		return false
	}
	return now.After(time.Unix(exp, 0).Add(ExpiryGrace))
}

// Socket is one registered WebSocket.
type Socket struct {
	registry *Registry
	id       uint64
	close    Closer

	mu     sync.Mutex
	claims auth.JWTClaims
	ended  bool
}

// Claims is a copy of the claims the socket is currently held to.
func (s *Socket) Claims() auth.JWTClaims {
	if s == nil {
		return auth.JWTClaims{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claims
}

// Expired reports whether the socket's token is past its grace at now. The
// persistent read loop asks this per frame so a frame arriving between sweeps
// is not served either.
func (s *Socket) Expired(now time.Time) bool {
	if s == nil {
		return false
	}
	return expired(s.Claims().Exp, now)
}

// CheckRefresh reports whether the socket may be held to claims from here on,
// without holding it to them. A caller that has more to change before the
// refresh takes effect asks this first and calls Refresh once the rest has
// succeeded.
//
// A token for a different subject is refused: a refresh keeps a socket open, it
// does not hand it to somebody else, and everything the socket was opened with
// — the function's ws_open, the identity the instance was bound to — was for
// the original one.
func (s *Socket) CheckRefresh(claims *auth.JWTClaims) error {
	if s == nil {
		return ErrNotRefreshable
	}
	if claims == nil {
		return ErrNoClaims
	}
	if claims.Sub != s.Claims().Sub {
		return ErrSubjectChanged
	}
	return nil
}

// Refresh holds the socket to a fresh token from here on, under the rules
// CheckRefresh states.
func (s *Socket) Refresh(claims *auth.JWTClaims) error {
	if err := s.CheckRefresh(claims); err != nil {
		return err
	}
	s.mu.Lock()
	s.claims = *claims
	s.mu.Unlock()
	return nil
}

// Unregister stops holding the socket. Call it when the socket's handler
// returns, however it returns.
func (s *Socket) Unregister() {
	if s == nil {
		return
	}
	s.registry.remove(s.id)
}

// end closes the socket once, however many sweeps find it, and reports
// whether this call was the one that closed it.
func (s *Socket) end(code int, reason string) bool {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return false
	}
	s.ended = true
	s.mu.Unlock()
	s.close(code, reason)
	return true
}
