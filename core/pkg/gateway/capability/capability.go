// Package capability mints and checks the tokens that open a function's
// WebSocket on the recipient's authority rather than the sender's.
//
// A messaging application wants a sender to reach a recipient's mailbox
// without the node that terminates the socket learning which account is
// sending. The recipient's device mints a capability — "whoever holds this may
// open rpc-router in namespace anchat, for mailbox M, until T" — and hands it
// to its correspondents; a sender opens the socket with it and no credential
// of its own. It is Signal's delivery token, shaped for this gateway.
//
// A token is a payload and an HMAC over it, keyed per namespace from the
// cluster secret (the same derivation the proxy hop's key uses, with its own
// purpose string). Every gateway of the cluster can check one, and nothing
// outside the cluster can mint one. It is opaque to its holder in the sense
// that matters: it cannot be altered or extended; its fields are readable,
// because nothing in them is secret from the recipient who minted it or the
// sender it was given to.
package capability

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/secrets"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

const (
	// MaxTTL is the longest a capability may live. A capability cannot be
	// narrowed once handed out, only revoked, so it is kept to a week: long
	// enough that a correspondent is not re-sent one every conversation, short
	// enough that a leaked one ends on its own. A device's revocation is
	// remembered exactly this long, so it covers everything the device issued.
	MaxTTL = auth.MaxDeviceIssuedLifetime

	// MinTTL is the shortest: less than a minute cannot survive the clock
	// drift between the node that mints and the node that checks.
	MinTTL = time.Minute

	// MaxResourceLength bounds what a capability names.
	MaxResourceLength = 256

	// maxTokenLength bounds a token before anything is parsed. A real one is
	// well under 1 KiB.
	maxTokenLength = 2048

	// keyPurposePrefix is the HKDF domain separator, with the namespace after
	// it, so each namespace has its own key and a token minted in one never
	// verifies in another.
	keyPurposePrefix = "orama-ws-capability-v1:"

	tokenVersion = 1

	// revocationPrefix is how a capability is named on the revocation list.
	revocationPrefix = "cap:"

	capabilityIDBytes = 16
)

var (
	// ErrInvalid is a token that is malformed, forged, expired, or for
	// another namespace or function. One error for all of them: which one it
	// was is of use only to somebody probing.
	ErrInvalid = errors.New("the capability is not valid for this function")

	// ErrNoIssuer is a mint from a session bound to no device. A capability
	// is revoked with the device that issued it, so it needs one.
	ErrNoIssuer = errors.New("a capability is issued by a device: the calling session is bound to none")
)

// Claims is what a capability grants.
type Claims struct {
	Version      int    `json:"v"`
	Namespace    string `json:"ns"`
	Function     string `json:"fn"`
	Resource     string `json:"res"`
	IssuerDevice string `json:"iss"`
	ID           string `json:"cid"`
	IssuedAt     int64  `json:"iat"`
	ExpiresAt    int64  `json:"exp"`
}

// Authority mints and verifies capabilities. Every gateway in a cluster holds
// the same one, because it is derived from the cluster secret.
type Authority struct {
	clusterSecret string
}

// NewAuthority builds the cluster's authority.
func NewAuthority(clusterSecret string) (*Authority, error) {
	if strings.TrimSpace(clusterSecret) == "" {
		return nil, fmt.Errorf("no cluster secret: capabilities cannot be minted or checked")
	}
	return &Authority{clusterSecret: clusterSecret}, nil
}

func (a *Authority) key(namespace string) ([]byte, error) {
	key, err := secrets.DeriveKey(a.clusterSecret, keyPurposePrefix+namespace)
	if err != nil {
		return nil, fmt.Errorf("derive the capability key of %q: %w", namespace, err)
	}
	return key, nil
}

// Mint issues a capability for one function's WebSocket in one namespace.
func (a *Authority) Mint(namespace, function, resource, issuerDevice string, ttl time.Duration, now time.Time) (string, *Claims, error) {
	if issuerDevice == "" {
		return "", nil, ErrNoIssuer
	}
	if namespace == "" || function == "" {
		return "", nil, fmt.Errorf("a capability names a namespace and a function")
	}
	if len(resource) > MaxResourceLength {
		return "", nil, fmt.Errorf("the resource is %d bytes; at most %d", len(resource), MaxResourceLength)
	}
	if ttl < MinTTL || ttl > MaxTTL {
		return "", nil, fmt.Errorf("a capability lives between %s and %s (asked for %s)", MinTTL, MaxTTL, ttl)
	}
	id := make([]byte, capabilityIDBytes)
	if _, err := rand.Read(id); err != nil {
		return "", nil, fmt.Errorf("draw a capability id: %w", err)
	}
	claims := &Claims{
		Version: tokenVersion, Namespace: namespace, Function: function, Resource: resource,
		IssuerDevice: issuerDevice, ID: hex.EncodeToString(id),
		IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", nil, fmt.Errorf("encode the capability: %w", err)
	}
	key, err := a.key(namespace)
	if err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sign(key, payload))
	return token, claims, nil
}

// Verify checks a token for one function in one namespace, at now. It does
// not consult the revocation list; the caller does, with RevocationClaims.
func (a *Authority) Verify(token, namespace, function string, now time.Time) (*Claims, error) {
	claims, err := a.Parse(token, namespace)
	if err != nil {
		return nil, err
	}
	if claims.Function != function || now.Unix() >= claims.ExpiresAt {
		return nil, ErrInvalid
	}
	return claims, nil
}

// Parse checks that a token is a capability this cluster minted in namespace,
// whatever function it opens and whether or not it has expired.
func (a *Authority) Parse(token, namespace string) (*Claims, error) {
	if len(token) == 0 || len(token) > maxTokenLength {
		return nil, ErrInvalid
	}
	encoded, sig, ok := strings.Cut(token, ".")
	if !ok {
		return nil, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrInvalid
	}
	presented, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, ErrInvalid
	}
	// The key is the namespace the caller is asking for, never the one the
	// token claims: a token for another namespace fails the MAC here.
	key, err := a.key(namespace)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(presented, sign(key, payload)) {
		return nil, ErrInvalid
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrInvalid
	}
	if claims.Version != tokenVersion || claims.Namespace != namespace || claims.Function == "" ||
		claims.IssuerDevice == "" || claims.ID == "" {
		return nil, ErrInvalid
	}
	return &claims, nil
}

func sign(key, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return mac.Sum(nil)
}

// RevocationID is the name a capability is revoked by.
func RevocationID(namespace, id string) string {
	return revocationPrefix + namespace + ":" + id
}

// Grant is what the capability grants, as the function it opens reads it.
func (c *Claims) Grant() *serverless.CapabilityGrant {
	return &serverless.CapabilityGrant{
		ID: c.ID, Resource: c.Resource, IssuerDevice: c.IssuerDevice, ExpiresAt: c.ExpiresAt,
	}
}

// RevocationClaims is the capability as the revocation list and the socket
// sweeper see it: revoked by its own id, or by revoking the device that issued
// it, and expiring when it does. It has no subject: no account is named.
func (c *Claims) RevocationClaims() *auth.JWTClaims {
	return &auth.JWTClaims{
		Jti:       RevocationID(c.Namespace, c.ID),
		Did:       c.IssuerDevice,
		Namespace: c.Namespace,
		Iat:       c.IssuedAt,
		Exp:       c.ExpiresAt,
	}
}
