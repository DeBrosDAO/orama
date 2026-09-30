package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// CoordinationMACV2Header carries "<unix seconds>.<hex hmac>" over the v2
	// payload. A request is stamped with both this and CoordinationMACHeader so
	// a node still on the previous build, which reads only the latter, keeps
	// accepting it during a rolling upgrade.
	CoordinationMACV2Header = "X-Orama-Coordination-MAC-V2"

	// CoordinationNonceHeader carries the hex of the single-use random value the
	// v2 MAC covers.
	CoordinationNonceHeader = "X-Orama-Coordination-Nonce"

	// CoordinationMaxBody bounds the body a coordination request may carry. It is
	// the spawn handler's own cap: the verifier reads the body to hash it, so it
	// must not accept more than the handler would.
	CoordinationMaxBody = 1 << 20

	// AcceptLegacyCoordinationMAC lets a request stamped only with the v1 MAC
	// verify, for callers that do not need the body covered. It exists for the
	// rolling upgrade from the build that signs nothing else, and is removed in
	// the release after that one (docs/SECURITY.md, "Coordination MAC v2").
	AcceptLegacyCoordinationMAC = true

	// coordinationNonceBytes is the size of the random nonce a signer draws.
	coordinationNonceBytes = 16

	// coordinationReplayTTL is how long a nonce is remembered. A stamp is valid
	// for coordinationMaxSkew in either direction, so a nonce that has been
	// forgotten belongs to a stamp that would already be refused.
	coordinationReplayTTL = 2 * coordinationMaxSkew

	// coordinationReplayCapacity bounds the replay cache. Coordination is a
	// handful of calls per namespace operation; this is orders of magnitude above
	// a fleet's rate over the TTL and costs a few megabytes at the limit.
	coordinationReplayCapacity = 1 << 16
)

// CoordinationVersion says which stamp a request verified under.
type CoordinationVersion int

const (
	// CoordinationV1 covered method, path, query and time only. A request that
	// verified this way can have had its body replaced.
	CoordinationV1 CoordinationVersion = 1
	// CoordinationV2 also covered the body and a single-use nonce.
	CoordinationV2 CoordinationVersion = 2
)

// coordinationPayloadV2 is the exact string a v2 MAC covers: everything v1
// covers, plus the SHA-256 of the body and the nonce. The label is the one
// SignACME's payload uses; the two are keyed differently, so a stamp for one
// never verifies as the other.
func coordinationPayloadV2(method, path, query string, body []byte, nonce string, ts int64) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		"orama-coordination-v2",
		strings.ToUpper(method),
		path,
		query,
		hex.EncodeToString(sum[:]),
		nonce,
		strconv.FormatInt(ts, 10),
	}, "\n")
}

// SignCoordination stamps a request as coming from inside the cluster, with the
// v2 MAC and, beside it, the v1 MAC a not-yet-upgraded peer reads.
//
// The body is part of what the v2 MAC covers, so it must be complete before the
// request is signed; changing it afterwards makes the request fail verification.
func SignCoordination(key []byte, r *http.Request, now time.Time) error {
	if len(key) == 0 {
		return fmt.Errorf("no coordination key: this node has no cluster secret, so it cannot " +
			"prove to another node that this request came from inside the cluster")
	}
	body, err := signableBody(r)
	if err != nil {
		return err
	}
	raw := make([]byte, coordinationNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("cannot draw a coordination nonce: %w", err)
	}
	nonce := hex.EncodeToString(raw)
	ts := now.Unix()

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(coordinationPayloadV2(r.Method, r.URL.Path, r.URL.RawQuery, body, nonce, ts)))
	r.Header.Set(CoordinationNonceHeader, nonce)
	r.Header.Set(CoordinationMACV2Header, strconv.FormatInt(ts, 10)+"."+hex.EncodeToString(mac.Sum(nil)))
	signCoordinationV1(key, r, now)
	return nil
}

// signableBody returns the bytes r will send, leaving r able to send them. It
// reads the body once and puts an identical one back, so a request built
// without GetBody is signed over exactly what it then sends.
func signableBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("cannot read the body of the coordination request to %s: %w", r.URL.Path, err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return body, nil
}

// VerifyCoordination reports whether a request was stamped by something holding
// the cluster secret, under either stamp. Use CheckCoordination where the
// endpoint has to know which.
func VerifyCoordination(key []byte, r *http.Request, now time.Time) bool {
	_, ok := CheckCoordination(key, r, now)
	return ok
}

// CheckCoordination verifies a coordination request and says which stamp it
// carried.
//
// It answers false for every reason: no key on this side, no stamp, a malformed
// one, a stale or future timestamp, a MAC over a different request or body than
// the one that arrived, or a nonce already seen. A v2 stamp that fails is never
// retried as v1: a request whose body was swapped still carries the v1 stamp
// it was signed with.
//
// A v2 verification reads the body (at most CoordinationMaxBody) and puts it
// back, so the handler reads it as if nothing had. The nonce is consumed only
// once the MAC is right, so nothing without the key can fill the replay cache.
func CheckCoordination(key []byte, r *http.Request, now time.Time) (CoordinationVersion, bool) {
	if len(key) == 0 {
		return 0, false
	}
	if r.Header.Get(CoordinationMACV2Header) != "" {
		return CoordinationV2, verifyCoordinationV2(key, r, now)
	}
	if AcceptLegacyCoordinationMAC && verifyCoordinationV1(key, r, now) {
		return CoordinationV1, true
	}
	return 0, false
}

func verifyCoordinationV2(key []byte, r *http.Request, now time.Time) bool {
	stamp, sig, ok := strings.Cut(strings.TrimSpace(r.Header.Get(CoordinationMACV2Header)), ".")
	if !ok {
		return false
	}
	ts, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return false
	}
	if skew := now.Sub(time.Unix(ts, 0)); skew > coordinationMaxSkew || skew < -coordinationMaxSkew {
		return false
	}
	nonce := r.Header.Get(CoordinationNonceHeader)
	if raw, err := hex.DecodeString(nonce); err != nil || len(raw) != coordinationNonceBytes {
		return false
	}
	presented, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	body, err := takeBody(r)
	if err != nil {
		return false
	}
	expected := hmac.New(sha256.New, key)
	expected.Write([]byte(coordinationPayloadV2(r.Method, r.URL.Path, r.URL.RawQuery, body, nonce, ts)))
	if !hmac.Equal(presented, expected.Sum(nil)) {
		return false
	}
	return coordinationReplays.firstUse(nonce, now)
}

// takeBody reads r's body, at most CoordinationMaxBody, and puts it back.
func takeBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, CoordinationMaxBody+1))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if len(body) > CoordinationMaxBody {
		return nil, fmt.Errorf("coordination body over %d bytes", CoordinationMaxBody)
	}
	return body, nil
}

// replayCache remembers nonces for coordinationReplayTTL, in a bounded,
// insertion-ordered set. When it is full of nonces that have not expired it
// refuses new ones rather than forgetting an old one: forgetting would make a
// flood of valid requests a way to reopen replay, and only a holder of the
// cluster secret can produce one.
type replayCache struct {
	mu       sync.Mutex
	seen     map[string]time.Time
	order    []string
	capacity int
	ttl      time.Duration
}

var coordinationReplays = newReplayCache(coordinationReplayCapacity, coordinationReplayTTL)

func newReplayCache(capacity int, ttl time.Duration) *replayCache {
	return &replayCache{seen: make(map[string]time.Time), capacity: capacity, ttl: ttl}
}

// firstUse records nonce and reports true, or reports false if it was already
// seen within the TTL or there is no room to remember it.
func (c *replayCache) firstUse(nonce string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.order) > 0 {
		oldest := c.order[0]
		if now.Sub(c.seen[oldest]) <= c.ttl {
			break
		}
		delete(c.seen, oldest)
		c.order = c.order[1:]
	}
	if _, dup := c.seen[nonce]; dup {
		return false
	}
	if len(c.seen) >= c.capacity {
		return false
	}
	c.seen[nonce] = now
	c.order = append(c.order, nonce)
	return true
}
