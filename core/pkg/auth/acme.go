package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// acmeChallengeKeyPurpose is the HKDF domain separator for the key Caddy signs
// its DNS-01 present and cleanup calls with.
//
// It is deliberately not the coordination key. The request is stamped with
// SignACME / VerifyACME, which cover the body; Caddy terminates TLS for the
// internet, and a Caddy holding the coordination key could ask any node to
// spawn or repair a namespace. This key authorises one thing: publishing an
// _acme-challenge TXT record under the cluster's domain.
const acmeChallengeKeyPurpose = "acme-challenge"

// ACMEChallengeKey derives the key both Caddy and the gateway use for the ACME
// DNS-01 endpoints. Install writes it where Caddy reads it; the gateway derives
// it from the cluster secret it already holds. The secret is trimmed for the
// reason CoordinationKey gives.
func ACMEChallengeKey(clusterSecret string) ([]byte, error) {
	key, err := secrets.DeriveKey(strings.TrimSpace(clusterSecret), acmeChallengeKeyPurpose)
	if err != nil {
		return nil, fmt.Errorf("no ACME challenge key: nothing can prove a DNS-01 request came from this node's Caddy: %w", err)
	}
	return key, nil
}

const (
	// ACMEMACV2Header carries "<unix seconds>.<hex hmac>" over the nonced
	// payload. SignACME sets it beside the unnonced stamp in
	// CoordinationMACHeader, which a gateway on the previous build reads.
	ACMEMACV2Header = "X-Orama-ACME-MAC-V2"

	// ACMENonceHeader carries the hex of the single-use random value the nonced
	// stamp covers.
	ACMENonceHeader = "X-Orama-ACME-Nonce"

	// AcceptLegacyACMEMAC lets a call stamped only with the unnonced MAC
	// verify, for a Caddy built before the nonce (the gateway and Caddy on a
	// node are upgraded one after the other, so either can be the newer for a
	// while). While it is set, a captured unnonced present or cleanup can be
	// replayed inside the stamp's window, which is how a cleanup deletes a
	// TXT record mid-challenge. It is removed in the release after the one that
	// introduced the nonce (docs/SECURITY.md, "ACME DNS-01").
	AcceptLegacyACMEMAC = true
)

// acmeReplays remembers the nonces of the ACME calls this process has served.
var acmeReplays = newReplayCache(coordinationReplayCapacity, coordinationReplayTTL)

// acmePayload is the exact string an unnonced ACME present or cleanup MAC
// covers.
//
// The body hash is in it. The coordination MAC covers method, path and query
// only, and these endpoints take the record they publish from the body: a MAC
// captured for one TXT record could be replayed with another name.
func acmePayload(method, path, query string, body []byte, ts int64) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		"orama-coordination-v2",
		strings.ToUpper(method),
		path,
		query,
		hex.EncodeToString(sum[:]),
		strconv.FormatInt(ts, 10),
	}, "\n")
}

// acmePayloadNonced is acmePayload plus the nonce, under its own label so one
// MAC is never valid as the other. A captured present or cleanup is good for
// the one call it was made for: replaying a cleanup after the challenge was
// presented again would delete a record the CA is about to read.
func acmePayloadNonced(method, path, query string, body []byte, nonce string, ts int64) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{
		"orama-acme-v2",
		strings.ToUpper(method),
		path,
		query,
		hex.EncodeToString(sum[:]),
		nonce,
		strconv.FormatInt(ts, 10),
	}, "\n")
}

// SignACME stamps a DNS-01 present or cleanup call with the nonced MAC and,
// beside it, the unnonced one a gateway on the previous build reads. body is
// the bytes the request will send; the MAC does not match a different body.
func SignACME(key []byte, r *http.Request, body []byte, now time.Time) error {
	if len(key) == 0 {
		return fmt.Errorf("no ACME challenge key: this node cannot prove a DNS-01 request came from its Caddy")
	}
	raw := make([]byte, coordinationNonceBytes)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Errorf("cannot draw an ACME nonce: %w", err)
	}
	nonce := hex.EncodeToString(raw)
	ts := now.Unix()
	r.Header.Set(ACMENonceHeader, nonce)
	setCoordinationStamp(key, r, ACMEMACV2Header, ts, acmePayloadNonced(r.Method, r.URL.Path, r.URL.RawQuery, body, nonce, ts))
	if legacyStampsAccepted() {
		setCoordinationStamp(key, r, CoordinationMACHeader, ts, acmePayload(r.Method, r.URL.Path, r.URL.RawQuery, body, ts))
	}
	return nil
}

// VerifyACME reports whether r was stamped by SignACME over body. A call with
// the nonced stamp is checked only against it and consumes its nonce; a call
// with only the unnonced one is checked as before while AcceptLegacyACMEMAC is
// set. A nonced stamp that fails is never retried as the unnonced one.
func VerifyACME(key []byte, r *http.Request, body []byte, now time.Time) bool {
	if len(key) == 0 {
		return false
	}
	if r.Header.Get(ACMEMACV2Header) != "" {
		return verifyACMENonced(key, r, body, now)
	}
	return AcceptLegacyACMEMAC && legacyStampsAccepted() && verifyACMEUnnonced(key, r, body, now)
}

func verifyACMEUnnonced(key []byte, r *http.Request, body []byte, now time.Time) bool {
	ts, sig, ok := parseStamp(r.Header.Get(CoordinationMACHeader), now, coordinationMaxSkew)
	if !ok {
		return false
	}
	expected := hmac.New(sha256.New, key)
	expected.Write([]byte(acmePayload(r.Method, r.URL.Path, r.URL.RawQuery, body, ts)))
	return hmac.Equal(sig, expected.Sum(nil))
}

func verifyACMENonced(key []byte, r *http.Request, body []byte, now time.Time) bool {
	ts, sig, ok := parseStamp(r.Header.Get(ACMEMACV2Header), now, coordinationMaxSkew)
	if !ok || !madeAfterProcessStart(ts, now) {
		return false
	}
	nonce := r.Header.Get(ACMENonceHeader)
	if raw, err := hex.DecodeString(nonce); err != nil || len(raw) != coordinationNonceBytes {
		return false
	}
	expected := hmac.New(sha256.New, key)
	expected.Write([]byte(acmePayloadNonced(r.Method, r.URL.Path, r.URL.RawQuery, body, nonce, ts)))
	if !hmac.Equal(sig, expected.Sum(nil)) {
		return false
	}
	return acmeReplays.firstUse(nonce, now)
}

// parseStamp splits "<unix seconds>.<hex signature>" and checks the time is
// within skew of now in either direction (a future timestamp is as much a sign
// of a forged stamp as an old one).
func parseStamp(value string, now time.Time, skew time.Duration) (int64, []byte, bool) {
	stamp, sigHex, ok := strings.Cut(strings.TrimSpace(value), ".")
	if !ok {
		return 0, nil, false
	}
	ts, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return 0, nil, false
	}
	if d := now.Sub(time.Unix(ts, 0)); d > skew || d < -skew {
		return 0, nil, false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return 0, nil, false
	}
	return ts, sig, true
}
