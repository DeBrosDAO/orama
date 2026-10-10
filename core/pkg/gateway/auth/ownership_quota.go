package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// The per-wallet namespace cap (cluster setting max_namespaces_per_wallet) is
// decided where an owner grant is written, not by a count read beforehand: two
// writers that each counted first would each find room. Creating a namespace
// carries the count in its owner-grant INSERT; transferring one carries it in
// the UPDATE that moves the owner row.

// ownedByWalletSQL counts the live owner grants a wallet holds: the namespaces
// it owns. It is the subquery of both statements that decide the cap, and the
// count the pre-checks read.
const ownedByWalletSQL = `SELECT COUNT(*) FROM grants AS g
		   JOIN principals AS p ON p.id = g.principal_id
		  WHERE p.type = 'wallet' AND p.identifier = ?
		    AND g.role = 'owner' AND g.revoked_at IS NULL`

// ErrNamespaceQuota is returned when a transfer would give a wallet more
// namespaces than the cluster lets one wallet own. Nothing is written.
type ErrNamespaceQuota struct {
	Wallet string
	Cap    int
}

func (e *ErrNamespaceQuota) Error() string {
	return fmt.Sprintf("wallet %s already owns the most namespaces it may (%d), so it cannot be handed another; "+
		"it has to delete or hand one on first", e.Wallet, e.Cap)
}

// requireRoomUnderCap refuses a wallet already at its cap, before a transfer
// writes anything. It is the cheap answer for the ordinary case; the transfer
// statement decides the racing one.
func (s *Service) requireRoomUnderCap(ctx context.Context, wallet string, walletCap int) error {
	owned, err := s.CountNamespacesOwnedBy(ctx, wallet)
	if err != nil {
		return err
	}
	if owned >= walletCap {
		return &ErrNamespaceQuota{Wallet: NormalizeWallet(wallet), Cap: walletCap}
	}
	return nil
}

// moveOwnerGrant hands the namespace's live owner grant to newOwnerID in one
// statement whose WHERE carries the new owner's cap, and reports whether a row
// moved. Raft applies one statement at a time, so the count in it sees every
// owner grant committed before it.
func (s *Service) moveOwnerGrant(ctx context.Context, namespace string, nsID, newOwnerID interface{}, fromWallet, toWallet string, walletCap int) (bool, error) {
	res, err := s.db.Exec(client.WithInternalAuth(ctx),
		`UPDATE grants SET principal_id = ?, created_by = ?, created_at = datetime('now')
		  WHERE namespace_id = ? AND role = 'owner' AND revoked_at IS NULL
		    AND (`+ownedByWalletSQL+`) < ?`,
		newOwnerID, fromWallet, nsID, toWallet, walletCap)
	if err != nil {
		return false, fmt.Errorf("failed to transfer namespace %q: %w", namespace, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to confirm the transfer of namespace %q: %w", namespace, err)
	}
	return affected > 0, nil
}

// explainUnmovedOwner says why a transfer moved nothing: the new owner reached
// its cap between the pre-check and the statement (the previous owner's admin
// row, written ahead of the move, is taken back), or the namespace lost its
// owner under the transfer.
func (s *Service) explainUnmovedOwner(ctx context.Context, namespace string, nsID interface{}, fromWallet, toWallet string, walletCap int) error {
	var quota *ErrNamespaceQuota
	switch err := s.requireRoomUnderCap(ctx, toWallet, walletCap); {
	case err == nil:
		return fmt.Errorf("namespace %q has no owner to transfer", namespace)
	case !errors.As(err, &quota):
		return err
	}
	fromID, err := s.ensurePrincipal(ctx, s.keyORM().Database(), PrincipalWallet, fromWallet, "", fromWallet)
	if err != nil {
		return fmt.Errorf("%s is at its namespace cap, and the previous owner's place could not be read back: %w", toWallet, err)
	}
	if err := retireNonOwnerGrants(ctx, s.keyORM().Database(), fromID, nsID); err != nil {
		return fmt.Errorf("%s is at its namespace cap, and the admin grant written for the previous owner could not be taken back: %w", toWallet, err)
	}
	return &ErrNamespaceQuota{Wallet: NormalizeWallet(toWallet), Cap: walletCap}
}
