package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// One node asking another to do something — spawn a namespace's services,
// repair an under-provisioned cluster — was authenticated by a header whose
// value is a constant in this repository:
//
//	X-Orama-Internal-Auth: namespace-coordination
//
// plus a check that the source address is on the WireGuard overlay. Neither is
// a credential. The string is public, and being on the overlay is not a
// privilege: every namespace's services are on that mesh, so any tenant
// workload that can reach a node's gateway port could spawn or stop services
// for any namespace on it. That is the same argument that took the OramaOS
// agent's "it only listens on WireGuard" comment apart.
//
// A coordination request now carries a MAC over what it is asking for, keyed by
// a value derived from the cluster secret. A caller that does not hold the
// cluster secret cannot produce one, and a MAC captured from one request cannot
// be replayed onto a different method, path or namespace.
//
// This is not node *identity* — every node holds the cluster secret, so any
// node can sign for any other. Node identity is the node-principal work; this
// closes the gap between "anything on the mesh" and "anything in the cluster".

const (
	// CoordinationMACHeader carries "<unix seconds>.<hex hmac>".
	CoordinationMACHeader = "X-Orama-Coordination-MAC"

	// coordinationKeyPurpose is the HKDF domain separator, so this key is
	// unrelated to the internal-auth hop, TURN, push and secrets keys derived
	// from the same cluster secret.
	coordinationKeyPurpose = "internal-coordination"

	// coordinationMaxSkew bounds replay. A coordination call is one request
	// over the mesh, so a minute is generous; it is there for clock drift
	// between nodes, not for network delay.
	coordinationMaxSkew = 60 * time.Second
)

// CoordinationKey derives the key both ends of a coordination call use.
//
// The secret is trimmed first, and that is load-bearing rather than tidy: it is
// read from a file, and one node's copy having a trailing newline while
// another's does not would derive two different keys and every coordination
// call between them would fail a MAC check for no visible reason. The same
// trap took `get_secret` down for days (bugboard #837).
func CoordinationKey(clusterSecret string) ([]byte, error) {
	key, err := secrets.DeriveKey(strings.TrimSpace(clusterSecret), coordinationKeyPurpose)
	if err != nil {
		return nil, fmt.Errorf("no coordination key: this node cannot prove a request came from "+
			"inside the cluster: %w", err)
	}
	return key, nil
}

// coordinationPayload is the exact string a MAC covers.
//
// The query string is in it because the namespace travels there: without it, a
// MAC for `?namespace=mine` would be replayable onto `?namespace=yours`, which
// is the whole thing this is for.
func coordinationPayload(method, path, query string, ts int64) string {
	return strings.Join([]string{
		"orama-coordination-v1",
		strings.ToUpper(method),
		path,
		query,
		strconv.FormatInt(ts, 10),
	}, "\n")
}

// SignCoordination stamps a request as coming from inside the cluster.
func SignCoordination(key []byte, r *http.Request, now time.Time) error {
	if len(key) == 0 {
		return fmt.Errorf("no coordination key: this node has no cluster secret, so it cannot " +
			"prove to another node that this request came from inside the cluster")
	}
	ts := now.Unix()
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(coordinationPayload(r.Method, r.URL.Path, r.URL.RawQuery, ts)))
	r.Header.Set(CoordinationMACHeader, strconv.FormatInt(ts, 10)+"."+hex.EncodeToString(mac.Sum(nil)))
	return nil
}

// VerifyCoordination reports whether a request was stamped by something holding
// the cluster secret.
//
// It answers false for every reason: no key on this side, no stamp, a malformed
// stamp, a stale or future timestamp, or a MAC over a different request than
// the one that arrived.
func VerifyCoordination(key []byte, r *http.Request, now time.Time) bool {
	if len(key) == 0 {
		return false
	}
	stamp, sig, ok := strings.Cut(strings.TrimSpace(r.Header.Get(CoordinationMACHeader)), ".")
	if !ok {
		return false
	}
	ts, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return false
	}
	// Both directions: a future timestamp is as much a sign of a forged stamp
	// as an old one.
	if skew := now.Sub(time.Unix(ts, 0)); skew > coordinationMaxSkew || skew < -coordinationMaxSkew {
		return false
	}

	expected := hmac.New(sha256.New, key)
	expected.Write([]byte(coordinationPayload(r.Method, r.URL.Path, r.URL.RawQuery, ts)))
	presented, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(presented, expected.Sum(nil))
}
