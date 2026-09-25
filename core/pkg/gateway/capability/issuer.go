package capability

import (
	"context"
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
	if i.now().Unix() >= claims.ExpiresAt || i.revocations.Denies(claims.RevocationClaims(), nil) {
		return nil
	}
	// Kept through the sweeper's grace past expiry, which is how long a socket
	// opened with the capability may stay open.
	expiresAt := claims.ExpiresAt + int64(wssession.ExpiryGrace/time.Second)
	if err := i.revocations.RevokeToken(ctx, RevocationID(namespace, claims.ID), expiresAt, "capability revoked"); err != nil {
		return fmt.Errorf("revoke capability %s in %q: %w", claims.ID, namespace, err)
	}
	return nil
}
