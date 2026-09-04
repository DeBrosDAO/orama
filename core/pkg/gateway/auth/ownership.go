package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// ErrNamespaceOwnedByAnother is returned when a wallet asks for credentials in
// a namespace another wallet already owns.
//
// Ownership used to be a side effect of minting a key: GetOrCreateAPIKey
// inserted the caller into namespace_ownership unconditionally, so any wallet
// that signed a fresh nonce and named an existing namespace in the body of
// /v1/auth/verify became a co-owner of it. That row satisfied the namespace
// gate, which marked the caller a confirmed owner, which granted a wallet JWT
// admin — and the key handed back in the same response was minted with no
// scopes, which the read path also treated as admin.
//
// Ownership is checked before anything is written now, and a namespace that
// already has a wallet owner accepts no second one.
type ErrNamespaceOwnedByAnother struct {
	Namespace string
}

func (e *ErrNamespaceOwnedByAnother) Error() string {
	return fmt.Sprintf("namespace %q belongs to another wallet", e.Namespace)
}

// ClaimNamespaceOwnership records wallet as the sole wallet owner of a
// namespace, or confirms it already is.
//
// A namespace has at most one wallet owner. The first wallet to ask for
// credentials in a namespace nobody owns is creating it and becomes the owner;
// a wallet asking in a namespace someone else owns is refused before a
// credential exists to hand back.
//
// The read cannot decide this on its own — two wallets arriving together both
// see no owner and both insert — so the insert is allowed to fail against the
// unique index migration 043 adds, and the owner is read back to say who won.
func (s *Service) ClaimNamespaceOwnership(ctx context.Context, db client.DatabaseClient, nsID interface{}, namespace, wallet string) error {
	owner := NormalizeWallet(wallet)
	internalCtx := client.WithInternalAuth(ctx)

	existing, err := s.walletOwnerOf(internalCtx, db, nsID)
	if err != nil {
		return fmt.Errorf("failed to read the owner of namespace %q: %w", namespace, err)
	}
	if existing != "" {
		if existing != owner {
			return &ErrNamespaceOwnedByAnother{Namespace: namespace}
		}
		return nil
	}

	if _, err := db.Query(internalCtx,
		"INSERT INTO namespace_ownership(namespace_id, owner_type, owner_id) VALUES (?, 'wallet', ?)",
		nsID, owner,
	); err != nil {
		// Either another wallet won the race for a namespace neither owned, or
		// the write genuinely failed. The owner says which.
		winner, readErr := s.walletOwnerOf(internalCtx, db, nsID)
		if readErr == nil && winner != "" && winner != owner {
			return &ErrNamespaceOwnedByAnother{Namespace: namespace}
		}
		if readErr == nil && winner == owner {
			return nil
		}
		return fmt.Errorf("failed to record ownership of namespace %q: %w", namespace, err)
	}
	return nil
}

// walletOwnerOf returns the namespace's wallet owner, or "" when it has none.
func (s *Service) walletOwnerOf(internalCtx context.Context, db client.DatabaseClient, nsID interface{}) (string, error) {
	res, err := db.Query(internalCtx,
		"SELECT owner_id FROM namespace_ownership WHERE namespace_id = ? AND owner_type = 'wallet' LIMIT 1",
		nsID,
	)
	if err != nil {
		return "", err
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return "", nil
	}
	return strings.TrimSpace(getStringVal(res.Rows[0][0])), nil
}

// RequireNamespaceOwner refuses unless the wallet owns the namespace, taking
// ownership of it when nobody does.
//
// A login handler calls this as soon as the signature and the nonce are
// settled, before it issues a JWT, mints a key or triggers provisioning. Doing
// it there rather than only inside GetOrCreateAPIKey means a wallet that does
// not own the namespace causes no writes at all: no refresh token row, no
// cluster provisioning for someone else's namespace.
func (s *Service) RequireNamespaceOwner(ctx context.Context, wallet, namespace string) error {
	if s.keyORM() == nil {
		return fmt.Errorf("client not initialized")
	}
	if strings.TrimSpace(wallet) == "" {
		return fmt.Errorf("wallet is required")
	}

	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}
	return s.ClaimNamespaceOwnership(ctx, s.keyORM().Database(), nsID, namespace, wallet)
}
