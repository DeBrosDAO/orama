package auth

import (
	"context"
	"errors"
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
// already has an owner accepts no second one.
type ErrNamespaceOwnedByAnother struct {
	Namespace string
}

func (e *ErrNamespaceOwnedByAnother) Error() string {
	return fmt.Sprintf("namespace %q belongs to another wallet", e.Namespace)
}

// ClaimNamespaceOwnership records wallet as the sole owner of a namespace, or
// confirms it already is one of its members.
//
// A namespace has at most one owner. A wallet that already holds any live grant
// there is a member and passes. A wallet arriving at a namespace that has an
// owner is refused before a credential exists to hand back.
//
// The first wallet to sign in to a namespace that has no owner at all becomes
// its owner. That is the behaviour this replaces, kept deliberately: the `default`
// namespace is created by migration 001 with no owner, and it is where a wallet
// signs in before it owns anything, so refusing an unowned namespace here would
// mean nobody could sign in to a fresh cluster at all. The epic asks for
// ownership to be written only by namespace creation; that needs `default` to
// have an owner first, and is filed separately.
//
// The read cannot decide this on its own — two wallets arriving together both
// see no owner and both insert — so the insert is allowed to fail against the
// partial unique index migration 050 carries, and the owner is read back to say
// who won.
func (s *Service) ClaimNamespaceOwnership(ctx context.Context, db client.DatabaseClient, nsID interface{}, namespace, wallet string) error {
	owner := NormalizeWallet(wallet)

	_, err := s.GrantIn(ctx, db, nsID, PrincipalWallet, owner)
	switch {
	case err == nil:
		// Already a member, at whatever role. Signing in does not change it.
		return nil
	case !errors.Is(err, ErrNotAMember):
		return fmt.Errorf("failed to read the grants of namespace %q: %w", namespace, err)
	}

	current, err := s.ownerOf(ctx, db, nsID)
	if err != nil {
		return fmt.Errorf("failed to read the owner of namespace %q: %w", namespace, err)
	}
	if current != "" {
		return &ErrNamespaceOwnedByAnother{Namespace: namespace}
	}

	principalID, err := s.ensurePrincipal(ctx, db, PrincipalWallet, owner, "", "first sign-in")
	if err != nil {
		return err
	}

	if _, err := db.Query(client.WithInternalAuth(ctx),
		"INSERT INTO grants(principal_id, namespace_id, role, created_by) VALUES (?, ?, 'owner', 'first sign-in')",
		principalID, nsID,
	); err != nil {
		// Either another wallet won the race for a namespace neither owned, or
		// the write genuinely failed. The owner says which.
		winner, readErr := s.ownerOf(ctx, db, nsID)
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

// ownerOf returns the namespace's owner, or "" when it has none.
func (s *Service) ownerOf(ctx context.Context, db client.DatabaseClient, nsID interface{}) (string, error) {
	res, err := db.Query(client.WithInternalAuth(ctx),
		`SELECT p.identifier FROM grants AS g
		   JOIN principals AS p ON p.id = g.principal_id
		  WHERE g.namespace_id = ? AND g.role = 'owner' AND g.revoked_at IS NULL
		  LIMIT 1`,
		nsID)
	if err != nil {
		return "", err
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return "", nil
	}
	return strings.TrimSpace(getStringVal(res.Rows[0][0])), nil
}

// OwnerOf returns the wallet that owns a namespace, or "" when nobody does.
func (s *Service) OwnerOf(ctx context.Context, namespace string) (string, error) {
	if s.keyORM() == nil {
		return "", fmt.Errorf("client not initialized")
	}
	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}
	return s.ownerOf(ctx, s.keyORM().Database(), nsID)
}

// RequireNamespaceOwner refuses unless the wallet is a member of the namespace,
// taking ownership of it when nobody owns it.
//
// A login handler calls this as soon as the signature and the nonce are
// settled, before it issues a JWT, mints a key or triggers provisioning. Doing
// it there rather than only inside GetOrCreateAPIKey means a wallet with no
// grant causes no writes at all: no refresh token row, no cluster provisioning
// for someone else's namespace.
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

// TransferOwnership moves the owner grant from one wallet to another.
//
// It is the only way ownership moves, and it is one operation rather than a
// revoke and a grant: a namespace between the two would have no owner, and an
// unowned namespace is claimable by whoever signs in next. The old owner keeps
// a place in the namespace as an admin, because the alternative is that
// handing over a project locks you out of it in the same instant.
func (s *Service) TransferOwnership(ctx context.Context, namespace, from, to string) error {
	if s.db == nil {
		return fmt.Errorf("transferring ownership requires the rqlite client (SetRqliteClient): without an affected-row count a transfer that changed nothing looks like one that worked")
	}
	fromWallet, toWallet := NormalizeWallet(from), NormalizeWallet(to)
	if toWallet == "" {
		return fmt.Errorf("a namespace has to be transferred to somebody")
	}
	if fromWallet == toWallet {
		return fmt.Errorf("namespace %q already belongs to %s", namespace, toWallet)
	}

	nsID, err := s.resolveKeyNamespaceID(ctx, namespace)
	if err != nil {
		return fmt.Errorf("failed to resolve namespace %q: %w", namespace, err)
	}
	db := s.keyORM().Database()

	current, err := s.ownerOf(ctx, db, nsID)
	if err != nil {
		return fmt.Errorf("failed to read the owner of namespace %q: %w", namespace, err)
	}
	if current == "" {
		return fmt.Errorf("namespace %q has no owner to transfer", namespace)
	}
	if current != fromWallet {
		return &ErrNamespaceOwnedByAnother{Namespace: namespace}
	}

	newOwnerID, err := s.ensurePrincipal(ctx, db, PrincipalWallet, toWallet, "", fromWallet)
	if err != nil {
		return err
	}

	// The old owner becomes an admin first. Doing it after the owner grant
	// moved would leave a window where the previous owner holds nothing, and a
	// failure in between would make handing over a project a way to lose it.
	if err := s.writeGrant(ctx, GrantRequest{
		Namespace:     namespace,
		PrincipalType: PrincipalWallet,
		Identifier:    fromWallet,
		Role:          RoleAdmin,
		CreatedBy:     fromWallet,
	}); err != nil {
		return fmt.Errorf("failed to keep the previous owner as an admin of %q: %w", namespace, err)
	}

	// One statement, so the namespace is never ownerless: the partial unique
	// index allows exactly one live owner, and this row is it.
	res, err := s.db.Exec(client.WithInternalAuth(ctx),
		`UPDATE grants SET principal_id = ?, created_by = ?, created_at = datetime('now')
		  WHERE namespace_id = ? AND role = 'owner' AND revoked_at IS NULL`,
		newOwnerID, fromWallet, nsID)
	if err != nil {
		return fmt.Errorf("failed to transfer namespace %q: %w", namespace, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to confirm the transfer of namespace %q: %w", namespace, err)
	}
	if affected == 0 {
		return fmt.Errorf("namespace %q has no owner to transfer", namespace)
	}

	s.audit.Record(ctx, AuditEvent{
		Namespace: namespace,
		Actor:     fromWallet,
		Action:    AuditOwnerTransferred,
		Resource:  "wallet " + toWallet,
		Result:    AuditSuccess,
	})
	return nil
}

// CountNamespacesOwnedBy is the per-wallet namespace quota's input.
func (s *Service) CountNamespacesOwnedBy(ctx context.Context, wallet string) (int, error) {
	if s.keyORM() == nil {
		return 0, fmt.Errorf("client not initialized")
	}
	res, err := s.keyORM().Database().Query(client.WithInternalAuth(ctx),
		`SELECT COUNT(*) FROM grants AS g
		   JOIN principals AS p ON p.id = g.principal_id
		  WHERE p.type = 'wallet' AND p.identifier = ?
		    AND g.role = 'owner' AND g.revoked_at IS NULL`,
		NormalizeWallet(wallet))
	if err != nil {
		return 0, fmt.Errorf("failed to count the namespaces owned by %s: %w", wallet, err)
	}
	if res == nil || res.Count == 0 || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return 0, nil
	}
	return int(toInt64(res.Rows[0][0])), nil
}
