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
	// Compiled in does not mean accepted: while a LegacyFloor is installed the
	// v1 form is refused once every node of the cluster signs the newer ones.
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
	// CoordinationV2 also covered the body and a single-use nonce. It named the
	// node it was for but not which process on it.
	CoordinationV2 CoordinationVersion = 2
	// CoordinationV3 also covers the port the request was sent to, so a stamp
	// is good for one process on the node and not its siblings.
	CoordinationV3 CoordinationVersion = 3
)

// coordinationPayloadV2 is the exact string a v2 MAC covers: everything v1
// covers, plus the audience (the libp2p peer id of the node the request is for),
// the SHA-256 of the body and the nonce. The audience is not read from the
// request: the signer puts the node it is calling there and the verifier puts
// its own id there, so a stamp for one node never verifies at another, however
// the request is addressed.
func coordinationPayloadV2(method, audience, path, query string, body []byte, nonce string, ts int64) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		"orama-coordination-v2",
		strings.ToUpper(method),
		audience,
		path,
		query,
		hex.EncodeToString(sum[:]),
		nonce,
		strconv.FormatInt(ts, 10),
	}, "\n")
}

// SignCoordination stamps a request as coming from inside the cluster, with the
// v3 MAC and, beside it, the v2 and v1 MACs a not-yet-upgraded peer reads.
// audience is the peer id of the node the request is sent to; only that node
// verifies it, and only the process listening on the request URL's port.
//
// The body is part of what the v2 MAC covers, so it must be complete before the
// request is signed; changing it afterwards makes the request fail verification.
func SignCoordination(key []byte, r *http.Request, now time.Time, audience string) error {
	if len(key) == 0 {
		return fmt.Errorf("no coordination key: this node has no cluster secret, so it cannot " +
			"prove to another node that this request came from inside the cluster")
	}
	if audience == "" {
		return fmt.Errorf("no audience for the coordination request to %s: the node it is for "+
			"must be named, or any node could be made to accept it", r.URL.Path)
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

	r.Header.Set(CoordinationNonceHeader, nonce)
	setCoordinationStamp(key, r, CoordinationMACV3Header, ts,
		coordinationPayloadV3(r.Method, audience, requestPort(r), r.URL.Path, r.URL.RawQuery, body, nonce, ts))
	// The older stamps are written only while some node may need them: beside
	// the v3 stamp they are what a replayer strips the v3 stamp back to.
	if legacyStampsAccepted() {
		setCoordinationStamp(key, r, CoordinationMACV2Header, ts,
			coordinationPayloadV2(r.Method, audience, r.URL.Path, r.URL.RawQuery, body, nonce, ts))
		signCoordinationV1(key, r, now)
	}
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
func VerifyCoordination(key []byte, r *http.Request, now time.Time, audience string) bool {
	_, ok := CheckCoordination(key, r, now, audience)
	return ok
}

// VerifyCoordinationV2 reports whether a request carries a valid stamp that
// covers the body and a nonce: v3, or v2 while AcceptLegacyCoordinationV2 is
// set. Use it for a route whose parameters travel in the body or that changes
// what a service points at: the v1 stamp does not cover the body, so a
// stripped-v2 replay with a swapped body would pass VerifyCoordination.
func VerifyCoordinationV2(key []byte, r *http.Request, now time.Time, audience string) bool {
	v, ok := CheckCoordination(key, r, now, audience)
	return ok && v >= CoordinationV2
}

// CheckCoordination verifies a coordination request and says which stamp it
// carried. audience is this node's own libp2p peer id, from its configuration
// and never from the request: a v2 or v3 stamp is valid only if it was signed
// for it. A v3 stamp is also valid only for the port the connection arrived on
// (coordinationServedPort). A v1 stamp names no audience.
//
// It answers false for every reason: no key on this side, no stamp, a malformed
// one, a stale or future timestamp, a MAC over a different request or body than
// the one that arrived, a stamp signed for another node or made before this
// process started, or a nonce already seen. A v2 stamp that fails is never
// retried as v1: a request whose body was swapped still carries the v1 stamp
// it was signed with.
//
// A v2 verification reads the body (at most CoordinationMaxBody) and puts it
// back, so the handler reads it as if nothing had. The nonce is consumed only
// once the MAC is right, so nothing without the key can fill the replay cache.
func CheckCoordination(key []byte, r *http.Request, now time.Time, audience string) (CoordinationVersion, bool) {
	if len(key) == 0 {
		return 0, false
	}
	if r.Header.Get(CoordinationMACV3Header) != "" {
		return CoordinationV3, audience != "" && verifyCoordinationV3(key, r, now, audience)
	}
	if r.Header.Get(CoordinationMACV2Header) != "" {
		return CoordinationV2, AcceptLegacyCoordinationV2 && legacyStampsAccepted() && audience != "" && verifyCoordinationV2(key, r, now, audience)
	}
	if AcceptLegacyCoordinationMAC && legacyStampsAccepted() && verifyCoordinationV1(key, r, now) {
		return CoordinationV1, true
	}
	return 0, false
}

func verifyCoordinationV2(key []byte, r *http.Request, now time.Time, audience string) bool {
	return verifyCoordinationNonced(key, r, now, CoordinationMACV2Header,
		func(body []byte, nonce string, ts int64) string {
			return coordinationPayloadV2(r.Method, audience, r.URL.Path, r.URL.RawQuery, body, nonce, ts)
		})
}

// verifyCoordinationNonced checks the stamp in header, whose payload payload
// builds from the body, nonce and time the request carries.
func verifyCoordinationNonced(key []byte, r *http.Request, now time.Time, header string,
	payload func(body []byte, nonce string, ts int64) string) bool {
	stamp, sig, ok := strings.Cut(strings.TrimSpace(r.Header.Get(header)), ".")
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
	// The nonce cache is per process and empty after a restart, so a stamp made
	// before this process started cannot be told from a replay of one it already
	// served. Refusing it costs a sender whose clock runs behind this node's a
	// retry in the first seconds after a restart.
	if !madeAfterProcessStart(ts, now) {
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
	expected.Write([]byte(payload(body, nonce, ts)))
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

// coordinationProcessStart is when this process's nonce cache began empty. It
// carries a monotonic reading, so the time since it is immune to the wall clock
// being stepped.
var coordinationProcessStart = time.Now()

// madeAfterProcessStart reports whether a stamp made at unix second ts is
// later than this process's start. The threshold is now minus the monotonic
// time elapsed since start, so a wall clock stepped backwards after start
// neither moves it nor refuses all traffic. It is rounded up to the next second
// because a stamp carries whole seconds: one made in the start second may
// predate the process, and is refused.
func madeAfterProcessStart(ts int64, now time.Time) bool {
	start := now.Add(-time.Since(coordinationProcessStart))
	threshold := start.Unix()
	if start.Nanosecond() != 0 {
		threshold++
	}
	return ts >= threshold
}

// CoordinationStampsAcceptedAfter is the time after which a stamp made now is
// one this process accepts: the end of the second it started in. A process
// that signs and verifies its own stamps (a test binary; never two nodes)
// waits for it, or its first stamps are refused as possibly older than it.
func CoordinationStampsAcceptedAfter() time.Time {
	return coordinationProcessStart.Truncate(time.Second).Add(time.Second)
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
