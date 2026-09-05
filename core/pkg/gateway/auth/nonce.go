package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// Challenge-nonce lifecycle.
//
// A wallet signature only proves "the holder of this key signed this string".
// It says nothing about *when*, so on its own it is a permanent credential:
// anyone who observes one valid (wallet, nonce, signature) tuple can present it
// again forever. The nonce is what turns that signature into a single login.
//
// That only holds if the gateway actually consumes the nonce it issued. The
// contract is:
//
//	CreateNonce  — issues a random nonce bound to (namespace, wallet) with a
//	               short expiry, and records it.
//	ConsumeNonce — atomically claims that record exactly once. A second claim,
//	               a claim after the expiry, or a claim for a nonce this gateway
//	               never issued all fail.
//
// Consumption is a single conditional UPDATE whose affected-row count is the
// lock, so two concurrent requests carrying the same nonce cannot both succeed:
// exactly one UPDATE matches the row while used_at is still NULL.
var (
	// ErrNonceInvalid means the challenge cannot be consumed: it was never
	// issued, has already been used, or has expired. The three cases are
	// deliberately indistinguishable to the caller so the endpoint does not
	// become an oracle for which wallets have outstanding challenges.
	ErrNonceInvalid = errors.New("authentication challenge is invalid, expired, or already used")

	// ErrNonceTransient means the challenge could not be consumed because the
	// registry was unreachable. It is distinct from ErrNonceInvalid so callers
	// can answer 503 rather than reporting a database outage as a bad
	// signature.
	ErrNonceTransient = errors.New("authentication challenge could not be verified")

	// ErrNonceConsumeNotConfigured means the service has no rqlite client, so
	// the conditional UPDATE cannot report an affected-row count and single-use
	// cannot be guaranteed. Consumption fails closed rather than degrading to a
	// non-atomic update, for the same reason RefreshToken refuses to rotate
	// without it.
	ErrNonceConsumeNotConfigured = errors.New("nonce consumption requires the rqlite client (SetRqliteClient)")
)

// nonceNamespace resolves the namespace name a challenge is filed under.
//
// CreateNonce and ConsumeNonce MUST agree on this mapping. If they disagree,
// a freshly issued nonce is unclaimable and every login breaks, so the
// defaulting lives here and nowhere else.
func (s *Service) nonceNamespace(namespace string) string {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = strings.TrimSpace(s.defaultNS)
	}
	if ns == "" {
		ns = "default"
	}
	return ns
}

// normalizeNonceWallet canonicalises a wallet address for nonce storage and
// lookup. See NormalizeWallet for why.
func normalizeNonceWallet(wallet string) string {
	return NormalizeWallet(wallet)
}

// lookupNamespaceID resolves a namespace name to its id without creating it.
//
// resolveNamespaceIDOn upserts, which is right when a caller is legitimately
// establishing a namespace but wrong on an authentication path: a failed login
// must not leave a namespace row behind.
func (s *Service) lookupNamespaceID(ctx context.Context, namespace string) (interface{}, error) {
	db := s.registryDatabase()
	if db == nil {
		return nil, fmt.Errorf("client not initialized")
	}
	res, err := db.Query(
		client.WithInternalAuth(ctx),
		"SELECT id FROM namespaces WHERE name = ? LIMIT 1",
		namespace,
	)
	if err != nil {
		return nil, err
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return nil, nil
	}
	return res.Rows[0][0], nil
}

// ConsumeNonce atomically claims a challenge nonce previously issued by
// CreateNonce, and is what makes a wallet signature usable exactly once.
//
// Callers must invoke it on every signature-authenticated path and refuse the
// request when it returns an error; verifying the signature alone proves
// nothing about freshness.
//
// Returns ErrNonceInvalid when the challenge is unknown, already used or
// expired, ErrNonceTransient when the registry could not be reached, and
// ErrNonceConsumeNotConfigured when single-use cannot be guaranteed at all.
func (s *Service) ConsumeNonce(ctx context.Context, wallet, nonce, namespace string) error {
	// No rqlite client means no affected-row count, which means no way to tell
	// "I claimed it" from "someone else already had". Refuse rather than
	// pretend.
	if s.db == nil {
		return ErrNonceConsumeNotConfigured
	}

	walletKey := normalizeNonceWallet(wallet)
	nonceKey := strings.TrimSpace(nonce)
	if walletKey == "" || nonceKey == "" {
		return ErrNonceInvalid
	}

	nsID, err := s.lookupNamespaceID(ctx, s.nonceNamespace(namespace))
	if err != nil {
		return fmt.Errorf("%w: resolve namespace: %v", ErrNonceTransient, err)
	}
	if nsID == nil {
		// The namespace does not exist, so this gateway cannot have issued a
		// challenge for it.
		return ErrNonceInvalid
	}

	// The conditional UPDATE is the whole security property. Every predicate
	// carries weight:
	//
	//	used_at IS NULL   — single use. Two concurrent claims race here and
	//	                    exactly one sees RowsAffected == 1.
	//	expires_at > now  — freshness. NULL fails the comparison and is
	//	                    therefore rejected: a row with no expiry is treated
	//	                    as unusable, not as valid forever.
	res, err := s.db.Exec(client.WithInternalAuth(ctx),
		`UPDATE nonces
		    SET used_at = datetime('now')
		  WHERE namespace_id = ?
		    AND wallet = ?
		    AND nonce = ?
		    AND used_at IS NULL
		    AND expires_at > datetime('now')`,
		nsID, walletKey, nonceKey)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNonceTransient, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%w: rows affected: %v", ErrNonceTransient, err)
	}
	if affected == 0 {
		return ErrNonceInvalid
	}

	return nil
}
