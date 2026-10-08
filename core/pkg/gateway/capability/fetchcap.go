package capability

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/secrets"
)

// Fetch capabilities (bugboard #266): the token that lets a client download one
// stored object without presenting an identity.
//
// A relayed fetch is only worth having if the node that serves the object never
// learns who is asking, and a JWT or an API key names its holder. A fetch
// capability instead says "whoever holds this may read this CID of this
// namespace until T". The owner's device mints a batch of them for one CID, so
// each fetch presents a token the serving node has not seen before.
//
// It is built as a WebSocket capability is — a payload and an HMAC under a key
// derived per namespace from the cluster secret — but under its own purpose
// string, so the two kinds never verify as one another.
//
// The revocation tag is the one place it differs. A WebSocket capability names
// the device that issued it in the clear; here that would hand the serving node
// a join key across every fetch one sender makes. The tag is the device id
// sealed under a key of its own with a fresh nonce per token, so two tokens of
// one device share nothing a reader can compare, yet the gateway that checks the
// token can still ask whether the device behind it was revoked.

const (
	// FetchMinTTL is the shortest a fetch capability lives. Fetches are
	// scheduled ahead of time; a token that dies in minutes is not one a sender
	// can be handed in a batch.
	FetchMinTTL = time.Hour

	// MaxFetchCapsPerMint bounds one mint. A sender uses one token per fetch, so
	// a batch is the unit; more than this is a sign the caller wants a
	// credential rather than a capability.
	MaxFetchCapsPerMint = 64

	// maxFetchCIDLength bounds the CID a token names. A canonical CID is far
	// shorter.
	maxFetchCIDLength = 256

	fetchKeyPurposePrefix    = "orama-storage-fetch-cap-v1:"
	fetchTagKeyPurposePrefix = "orama-storage-fetch-cap-tag-v1:"
	// fetchRevokeKeyPurposePrefix keys the revoke keys: a key of its own, so a
	// revoke key is never a fetch capability's MAC nor the other way round.
	fetchRevokeKeyPurposePrefix = "orama-storage-fetch-cap-revoke-v1:"

	fetchTokenVersion = 1
)

// ErrFetchInvalid is a fetch capability that is malformed, forged, expired, or
// for another namespace or CID. One error for all of them: which one it was is
// of use only to somebody probing.
var ErrFetchInvalid = errors.New("the fetch capability is not valid for this content")

// FetchClaims is what a fetch capability grants.
type FetchClaims struct {
	Version       int    `json:"v"`
	Namespace     string `json:"ns"`
	CID           string `json:"cid"`
	RevocationTag string `json:"rt"`
	ID            string `json:"id"`
	IssuedAt      int64  `json:"iat"`
	ExpiresAt     int64  `json:"exp"`
}

// MintFetch issues a capability to read cid of namespace, issued by
// issuerDevice. Call it once per token wanted: every token has its own id and
// its own revocation tag.
func (a *Authority) MintFetch(namespace, cid, issuerDevice string, ttl time.Duration, now time.Time) (string, *FetchClaims, error) {
	if issuerDevice == "" {
		return "", nil, ErrNoIssuer
	}
	if namespace == "" || cid == "" {
		return "", nil, fmt.Errorf("a fetch capability names a namespace and a CID")
	}
	if len(cid) > maxFetchCIDLength {
		return "", nil, fmt.Errorf("the CID is %d bytes; at most %d", len(cid), maxFetchCIDLength)
	}
	if ttl < FetchMinTTL || ttl > MaxTTL {
		return "", nil, fmt.Errorf("a fetch capability lives between %s and %s (asked for %s)", FetchMinTTL, MaxTTL, ttl)
	}
	id := make([]byte, capabilityIDBytes)
	if _, err := rand.Read(id); err != nil {
		return "", nil, fmt.Errorf("draw a fetch capability id: %w", err)
	}
	tag, err := a.sealTag(namespace, issuerDevice)
	if err != nil {
		return "", nil, err
	}
	claims := &FetchClaims{
		Version: fetchTokenVersion, Namespace: namespace, CID: cid, RevocationTag: tag,
		ID: hex.EncodeToString(id), IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", nil, fmt.Errorf("encode the fetch capability: %w", err)
	}
	key, err := a.keyFor(fetchKeyPurposePrefix, namespace)
	if err != nil {
		return "", nil, err
	}
	return sealToken(key, payload), claims, nil
}

// VerifyFetch checks a token for one CID in one namespace, at now. It does not
// consult the revocation list; the caller does, with FetchRevocationClaims.
//
// The error is ErrFetchInvalid for any token that is not good, and the key
// derivation's own error, which is not the token's fault, otherwise.
func (a *Authority) VerifyFetch(token, namespace, cid string, now time.Time) (*FetchClaims, error) {
	// The key is the namespace the caller is asking for, never the one the
	// token claims: a token for another namespace fails the MAC here.
	key, err := a.keyFor(fetchKeyPurposePrefix, namespace)
	if err != nil {
		return nil, err
	}
	payload, ok := openToken(token, key)
	if !ok {
		return nil, ErrFetchInvalid
	}
	var claims FetchClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrFetchInvalid
	}
	if claims.Version != fetchTokenVersion || claims.Namespace != namespace || claims.CID != cid ||
		claims.ID == "" || claims.RevocationTag == "" || now.Unix() >= claims.ExpiresAt {
		return nil, ErrFetchInvalid
	}
	return &claims, nil
}

// FetchRevocationClaims is the capability as the revocation list sees it:
// revoked by its own id, or by revoking the device that issued it, and expiring
// when it does. The device is opened from the tag here, in memory, and goes no
// further.
func (a *Authority) FetchRevocationClaims(c *FetchClaims) (*auth.JWTClaims, error) {
	device, err := a.openTag(c.Namespace, c.RevocationTag)
	if err != nil {
		return nil, err
	}
	return &auth.JWTClaims{
		Jti:       RevocationID(c.Namespace, c.ID),
		Did:       device,
		Namespace: c.Namespace,
		Iat:       c.IssuedAt,
		Exp:       c.ExpiresAt,
	}, nil
}

// RevokeKey is the proof that a fetch capability id was issued by this
// cluster for namespace: HMAC-SHA256 of the id under the namespace's revoke key,
// hex encoded. Mint returns it beside the token and revoking by id requires it,
// so only a holder of what a mint returned can write a revocation row; an id
// alone, which any caller can invent, cannot.
func (a *Authority) RevokeKey(namespace, id string) (string, error) {
	key, err := a.keyFor(fetchRevokeKeyPurposePrefix, namespace)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sign(key, []byte(id))), nil
}

// VerifyRevokeKey reports whether presented is the revoke key of id in namespace,
// in constant time. The error is the key derivation's own, which is not the
// caller's fault.
func (a *Authority) VerifyRevokeKey(namespace, id, presented string) (bool, error) {
	want, err := a.RevokeKey(namespace, id)
	if err != nil {
		return false, err
	}
	return hmac.Equal([]byte(presented), []byte(want)), nil
}

func (a *Authority) sealTag(namespace, device string) (string, error) {
	key, err := a.keyFor(fetchTagKeyPurposePrefix, namespace)
	if err != nil {
		return "", err
	}
	tag, err := secrets.Encrypt(device, key)
	if err != nil {
		return "", fmt.Errorf("seal the revocation tag: %w", err)
	}
	return tag, nil
}

// openTag returns the device a tag was sealed from. A tag that does not open
// was not sealed by this cluster, which a token that passed its MAC cannot
// carry; it is reported rather than treated as no device.
func (a *Authority) openTag(namespace, tag string) (string, error) {
	key, err := a.keyFor(fetchTagKeyPurposePrefix, namespace)
	if err != nil {
		return "", err
	}
	device, err := secrets.Decrypt(tag, key)
	if err != nil {
		return "", fmt.Errorf("open the revocation tag: %w", err)
	}
	return device, nil
}
