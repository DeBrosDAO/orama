package capability

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/wssession"
	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// Issuer is what a function reaches through capability_mint and
// capability_revoke: the cluster's authority, and the revocation list a
// revoked capability is put on.
type Issuer struct {
	authority   *Authority
	revocations *auth.RevocationList
	now         func() time.Time
}

// NewIssuer builds the issuer the host functions use.
func NewIssuer(authority *Authority, revocations *auth.RevocationList) (*Issuer, error) {
	if authority == nil || revocations == nil {
		return nil, fmt.Errorf("a capability issuer needs the cluster's authority and the revocation list")
	}
	return &Issuer{authority: authority, revocations: revocations, now: time.Now}, nil
}

// Mint implements serverless.CapabilityIssuer.
func (i *Issuer) Mint(_ context.Context, namespace, function, resource, issuerDevice string, ttl time.Duration) (*serverless.CapabilityGrant, string, error) {
	token, claims, err := i.authority.Mint(namespace, function, resource, issuerDevice, ttl, i.now())
	if err != nil {
		return nil, "", err
	}
	return claims.Grant(), token, nil
}

// Revoke implements serverless.CapabilityIssuer. It takes the capability
// itself, not its id: only a capability this namespace was really issued can
// be put on the list, and its entry lives as long as it does, and as long as a
// socket it opened may. An id
// alone would let a function write week-long rows of anything it liked into a
// table every gateway reloads every ten seconds.
//
// An expired capability opens nothing, and one already revoked is revoked;
// neither writes a row.
func (i *Issuer) Revoke(ctx context.Context, namespace, token string) error {
	claims, err := i.authority.Parse(strings.TrimSpace(token), namespace)
	if err != nil {
		return fmt.Errorf("not a capability of %q: %w", namespace, err)
	}
	if i.now().Unix() >= claims.ExpiresAt {
		return nil
	}
	revoked, err := i.revocations.Denies(claims.RevocationClaims(), nil)
	if err != nil {
		return fmt.Errorf("check whether capability %s in %q is already revoked: %w: %w",
			claims.ID, namespace, serverless.ErrCapabilityUnavailable, err)
	}
	if revoked {
		return nil
	}
	// Kept through the sweeper's grace past expiry, which is how long a socket
	// opened with the capability may stay open.
	expiresAt := claims.ExpiresAt + int64(wssession.ExpiryGrace/time.Second)
	if err := i.revocations.RevokeToken(ctx, RevocationID(namespace, claims.ID), expiresAt, "capability revoked"); err != nil {
		return fmt.Errorf("revoke capability %s in %q: %w: %w", claims.ID, namespace, serverless.ErrCapabilityUnavailable, err)
	}
	return nil
}

// ErrFetchRevoked is a fetch capability that was valid and has since been
// revoked, itself or through the device that issued it. It is told apart from
// ErrFetchInvalid because the holder has something to act on: ask for another.
var ErrFetchRevoked = errors.New("this fetch capability, or the device that issued it, was revoked")

// MintFetchCaps implements serverless.FetchCapIssuer: count capabilities to
// read cid of namespace, each its own token, all issued by issuerDevice.
func (i *Issuer) MintFetchCaps(_ context.Context, namespace, cid, issuerDevice string, count int, ttl time.Duration) ([]serverless.FetchCap, error) {
	if count < 1 || count > MaxFetchCapsPerMint {
		return nil, fmt.Errorf("a mint issues between 1 and %d fetch capabilities (asked for %d)", MaxFetchCapsPerMint, count)
	}
	now := i.now()
	caps := make([]serverless.FetchCap, 0, count)
	for range count {
		token, claims, err := i.authority.MintFetch(namespace, cid, issuerDevice, ttl, now)
		if err != nil {
			return nil, err
		}
		revokeKey, err := i.authority.RevokeKey(namespace, claims.ID)
		if err != nil {
			return nil, err
		}
		caps = append(caps, serverless.FetchCap{ID: claims.ID, Token: token, RevokeKey: revokeKey, ExpiresAt: claims.ExpiresAt})
	}
	return caps, nil
}

// ErrFetchRevokeKeyInvalid is a revoke by id whose revoke key is not the one the
// mint returned for that id.
var ErrFetchRevokeKeyInvalid = errors.New("the revoke key is not the one issued for this fetch capability")

// RevokeFetchCap refuses one fetch capability of namespace from now on, named by
// its id and proved by the revoke key the mint returned for it. The token's
// expiry is not known here, so the entry is kept for the longest a token lives.
//
// The proof is what keeps the table small: an id alone is a string any caller
// can invent, and every invented one would be a week-long row in a table every
// gateway reloads every few seconds. A capability already revoked writes no
// second row.
func (i *Issuer) RevokeFetchCap(ctx context.Context, namespace, id, revokeKey string) error {
	if !ValidFetchCapID(id) {
		return fmt.Errorf("%q is not a fetch capability id", id)
	}
	ok, err := i.authority.VerifyRevokeKey(namespace, id, revokeKey)
	if err != nil {
		return err
	}
	if !ok {
		return ErrFetchRevokeKeyInvalid
	}
	revocationID := RevocationID(namespace, id)
	denied, err := i.revocations.Denies(&auth.JWTClaims{Jti: revocationID}, nil)
	if err != nil {
		return fmt.Errorf("check whether fetch capability %s in %q is already revoked: %w", id, namespace, err)
	}
	if denied {
		return nil
	}
	expiresAt := i.now().Add(MaxTTL).Unix()
	if err := i.revocations.RevokeToken(ctx, revocationID, expiresAt, "fetch capability revoked"); err != nil {
		return fmt.Errorf("revoke fetch capability %s in %q: %w", id, namespace, err)
	}
	return nil
}

// CheckFetch verifies a fetch capability for cid of namespace and consults the
// revocation list. ErrFetchInvalid and ErrFetchRevoked are the token's; any
// other error is the gateway's own (no key, no revocation list) and the caller
// answers it as unavailable.
func (i *Issuer) CheckFetch(token, namespace, cid string) (*FetchClaims, error) {
	claims, err := i.authority.VerifyFetch(token, namespace, cid, i.now())
	if err != nil {
		return nil, err
	}
	revocation, err := i.authority.FetchRevocationClaims(claims)
	if err != nil {
		return nil, err
	}
	revoked, err := i.revocations.Denies(revocation, nil)
	if err != nil {
		return nil, fmt.Errorf("check whether fetch capability %s in %q is revoked: %w", claims.ID, namespace, err)
	}
	if revoked {
		return nil, ErrFetchRevoked
	}
	return claims, nil
}

// ValidFetchCapID reports whether id has the shape of a fetch capability id.
func ValidFetchCapID(id string) bool {
	if len(id) != hex.EncodedLen(capabilityIDBytes) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}
