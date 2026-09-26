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

// acmePayload is the exact string an ACME present or cleanup MAC covers.
//
// The body hash is in it. The coordination MAC covers method, path and query
// only, and these endpoints take the record they publish from the body: a MAC
// captured for one TXT record could be replayed with another name. Caddy and
// the gateway on a node are the same archive, so there is no older payload
// to keep accepting.
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

// SignACME stamps a DNS-01 present or cleanup call. body is the bytes the
// request will send; the MAC does not match a different body.
func SignACME(key []byte, r *http.Request, body []byte, now time.Time) error {
	if len(key) == 0 {
		return fmt.Errorf("no ACME challenge key: this node cannot prove a DNS-01 request came from its Caddy")
	}
	ts := now.Unix()
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(acmePayload(r.Method, r.URL.Path, r.URL.RawQuery, body, ts)))
	r.Header.Set(CoordinationMACHeader, strconv.FormatInt(ts, 10)+"."+hex.EncodeToString(mac.Sum(nil)))
	return nil
}

// VerifyACME reports whether r was stamped by SignACME over body.
func VerifyACME(key []byte, r *http.Request, body []byte, now time.Time) bool {
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
	if skew := now.Sub(time.Unix(ts, 0)); skew > coordinationMaxSkew || skew < -coordinationMaxSkew {
		return false
	}
	expected := hmac.New(sha256.New, key)
	expected.Write([]byte(acmePayload(r.Method, r.URL.Path, r.URL.RawQuery, body, ts)))
	presented, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(presented, expected.Sum(nil))
}
