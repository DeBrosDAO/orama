package clientkey

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ProofHeader is the request header Caddy puts the node's proof secret in. The Caddyfile sets it on
// every request it forwards to the gateway (which replaces any copy a client sent) and strips it
// from the ones it forwards elsewhere; the gateway's first middleware (Prover.Middleware) compares
// it and deletes it, so it never travels further than that.
const ProofHeader = "X-Orama-Caddy-Proof"

const (
	// minProofLen is the shortest secret the gateway accepts: the installer writes 32 random bytes
	// as 64 hex characters, and a file cut short by a write in progress or a mistake must not
	// become a secret anyone can guess.
	minProofLen = 32
	// maxProofFileBytes bounds what is read from the file.
	maxProofFileBytes = 512
	// proofReloadInterval is the least time between two reads of the file. A header that does not
	// match may be one written before the secret was rotated, so the file is read again, but a
	// caller that sends wrong values on purpose must not make a read of the disk per request.
	proofReloadInterval = 2 * time.Second
)

type provenKey struct{}

// Prover decides whether a request came through the node's own Caddy, by the secret Caddy puts in
// ProofHeader. The secret is made at install and again at every upgrade, so the file is read again
// when a header does not match what is held (at most once per proofReloadInterval).
type Prover struct {
	path   string
	report func(error)
	now    func() time.Time

	mu         sync.RWMutex
	secret     []byte
	lastReload time.Time
	// reloading lets one reload read the file at a time.
	reloading sync.Mutex
}

// NewProver reads the secret at path. A file that is missing or unusable is reported through
// report (which may be nil) and leaves the prover holding nothing, so no request is proven until
// the file is there: callers are then keyed by the connection's address, which is the safe side.
// report receives an error that names the path and never the content.
func NewProver(path string, report func(error)) *Prover {
	p := &Prover{path: path, report: report, now: time.Now}
	p.reload()
	return p
}

// Ready reports whether the prover holds a secret.
func (p *Prover) Ready() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.secret) > 0
}

// Verify reports whether got is the secret, compared in constant time. A nil Prover proves nothing.
func (p *Prover) Verify(got string) bool {
	if p == nil || got == "" {
		return false
	}
	if p.matches(got) {
		return true
	}
	return p.reload() && p.matches(got)
}

func (p *Prover) matches(got string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.secret) > 0 && subtle.ConstantTimeCompare([]byte(got), p.secret) == 1
}

// reload reads the file, at most once per proofReloadInterval, and reports whether what the
// prover holds changed. One reload runs at a time (reloading); the file is read outside mu, so
// requests that only compare are never held behind the disk, and a stream of wrong values costs
// each request a try of reloading and a look at the clock, not a wait.
func (p *Prover) reload() bool {
	if !p.reloading.TryLock() {
		return false
	}
	defer p.reloading.Unlock()
	now := p.now()
	p.mu.RLock()
	recent := !p.lastReload.IsZero() && now.Sub(p.lastReload) < proofReloadInterval
	p.mu.RUnlock()
	if recent {
		return false
	}
	secret, err := readProof(p.path)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastReload = now
	if err != nil {
		// The proof is gone only when the file is (a removed or emptied secret proves nothing any
		// more); a read that failed for another reason (too many open files) keeps the last good
		// secret, so a passing failure does not put every public caller in the loopback bucket.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errProofShort) {
			p.secret = nil
		}
		if p.report != nil {
			p.report(err)
		}
		return false
	}
	changed := string(secret) != string(p.secret)
	p.secret = secret
	return changed
}

// errProofShort is a proof file too short to be a secret the installer wrote.
var errProofShort = errors.New("the proof file holds no secret")

func readProof(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open the Caddy proof file %s (the installer writes it; until it is there every public caller is keyed by the loopback address): %w", path, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxProofFileBytes))
	if err != nil {
		return nil, fmt.Errorf("read the Caddy proof file %s: %w", path, err)
	}
	secret := strings.TrimSpace(string(raw))
	if len(secret) < minProofLen {
		return nil, fmt.Errorf("the Caddy proof file %s holds fewer than %d characters: %w", path, minProofLen, errProofShort)
	}
	return []byte(secret), nil
}

// Middleware is the first thing a request meets. It always deletes ProofHeader, so the secret
// reaches nothing below (no handler, no proxied service, no tenant's deployment), and marks the
// request as proven when the header carried the secret (a nil Prover proves nothing). A request that sends a wrong or forged
// value is not refused: it is a request that is not proven, which is what it is.
func (p *Prover) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(ProofHeader)
		r.Header.Del(ProofHeader)
		if got != "" && p.Verify(got) {
			r = r.WithContext(context.WithValue(r.Context(), provenKey{}, true))
		}
		next.ServeHTTP(w, r)
	})
}

// proven reports whether Middleware marked the request as one that came through the node's Caddy.
func proven(r *http.Request) bool {
	ok, _ := r.Context().Value(provenKey{}).(bool)
	return ok
}
