package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// testWalletCap is a cap no test but the quota ones comes near.
const testWalletCap = 10

// ownNamespaces makes wallet the owner of n more namespaces, ids 100 upward.
func ownNamespaces(t *testing.T, s *Service, db *sqliteDatabase, wallet string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		id := 100 + i
		if _, err := db.db.Exec(`INSERT INTO namespaces(id, name) VALUES (?, ?)`, id, fmt.Sprintf("owned-%d", id)); err != nil {
			t.Fatalf("create namespace: %v", err)
		}
		giveOwner(t, s, db, int64(id), wallet)
	}
}

func liveOwners(t *testing.T, db *sqliteDatabase, namespace string) []string {
	t.Helper()
	rows, err := db.db.Query(`SELECT p.identifier FROM grants g
		JOIN principals p ON p.id = g.principal_id
		JOIN namespaces n ON n.id = g.namespace_id
		WHERE n.name = ? AND g.role = 'owner' AND g.revoked_at IS NULL`, namespace)
	if err != nil {
		t.Fatalf("read owners: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func liveGrantRows(t *testing.T, db *sqliteDatabase) int {
	t.Helper()
	var n int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM grants WHERE revoked_at IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	return n
}

// A wallet at its cap cannot be handed another namespace: the owner keeps it,
// nothing is written, and the refusal names the cap.
func TestTransferOwnership_refusesARecipientAtItsCap(t *testing.T) {
	s, db, nsID := realRegistry(t)
	ctx := context.Background()
	giveOwner(t, s, db, nsID, "0xcreator")
	ownNamespaces(t, s, db, "0xfull", 2)
	before := liveGrantRows(t, db)

	err := s.TransferOwnership(ctx, "anchat", "0xcreator", "0xFULL", 2)

	var quota *ErrNamespaceQuota
	if !errors.As(err, &quota) {
		t.Fatalf("err = %v, want *ErrNamespaceQuota", err)
	}
	if quota.Cap != 2 || quota.Wallet != "0xfull" {
		t.Errorf("the refusal names %+v, want wallet 0xfull and cap 2", quota)
	}
	if owners := liveOwners(t, db, "anchat"); len(owners) != 1 || owners[0] != "0xcreator" {
		t.Errorf("owners of anchat after a refused transfer: %v", owners)
	}
	if after := liveGrantRows(t, db); after != before {
		t.Errorf("a refused transfer changed the live grants from %d to %d", before, after)
	}
}

func TestTransferOwnership_allowsARecipientBelowItsCap(t *testing.T) {
	s, db, nsID := realRegistry(t)
	giveOwner(t, s, db, nsID, "0xcreator")
	ownNamespaces(t, s, db, "0xroomy", 2)

	if err := s.TransferOwnership(context.Background(), "anchat", "0xcreator", "0xroomy", 3); err != nil {
		t.Fatalf("a wallet one under its cap was refused: %v", err)
	}
	owned, err := s.CountNamespacesOwnedBy(context.Background(), "0xroomy")
	if err != nil || owned != 3 {
		t.Errorf("the recipient owns %d, %v; want exactly its cap", owned, err)
	}
}

// What counts against the cap is the namespaces a wallet owns now: an owner
// grant that was revoked, and the admin and member places it holds in other
// people's namespaces, do not.
func TestTransferOwnership_countsOnlyLiveOwnership(t *testing.T) {
	s, db, nsID := realRegistry(t)
	ctx := context.Background()
	giveOwner(t, s, db, nsID, "0xcreator")
	ownNamespaces(t, s, db, "0xrecipient", 2)
	if _, err := db.db.Exec(`UPDATE grants SET revoked_at = datetime('now') WHERE namespace_id = 100`); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, GrantRequest{Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xrecipient", Role: RoleAdmin}); err != nil {
		t.Fatalf("add the recipient as a member: %v", err)
	}

	if err := s.TransferOwnership(ctx, "anchat", "0xcreator", "0xrecipient", 2); err != nil {
		t.Fatalf("a wallet owning one live namespace, with a revoked one and a membership beside it, was refused at a cap of 2: %v", err)
	}
}

// Two transfers can both pass the count read ahead of the write; the statement
// that moves the owner row decides, so the second finds the cap reached.
func TestMoveOwnerGrant_theStatementCarriesTheCap(t *testing.T) {
	s, db, nsID := realRegistry(t)
	ctx := context.Background()
	giveOwner(t, s, db, nsID, "0xcreator")
	ownNamespaces(t, s, db, "0xfull", 2)
	fullID, err := s.ensurePrincipal(ctx, db, PrincipalWallet, "0xfull", "", "test")
	if err != nil {
		t.Fatal(err)
	}

	moved, err := s.moveOwnerGrant(ctx, "anchat", nsID, fullID, "0xcreator", "0xfull", 2)
	if err != nil || moved {
		t.Fatalf("moved = %v, %v: the owner row went to a wallet at its cap", moved, err)
	}
	moved, err = s.moveOwnerGrant(ctx, "anchat", nsID, fullID, "0xcreator", "0xfull", 3)
	if err != nil || !moved {
		t.Fatalf("moved = %v, %v: a wallet under its cap was not handed the row", moved, err)
	}
}

// The previous owner's admin place is written ahead of the move. When the move
// then finds the cap reached, that place is taken back and the refusal is the
// quota, not "no owner".
func TestExplainUnmovedOwner_takesBackThePreviousOwnersPlace(t *testing.T) {
	s, db, nsID := realRegistry(t)
	ctx := context.Background()
	giveOwner(t, s, db, nsID, "0xcreator")
	ownNamespaces(t, s, db, "0xfull", 2)
	if err := s.writeGrant(ctx, GrantRequest{Namespace: "anchat", PrincipalType: PrincipalWallet, Identifier: "0xcreator", Role: RoleAdmin}); err != nil {
		t.Fatal(err)
	}

	err := s.explainUnmovedOwner(ctx, "anchat", nsID, "0xcreator", "0xfull", 2)

	var quota *ErrNamespaceQuota
	if !errors.As(err, &quota) {
		t.Fatalf("err = %v, want *ErrNamespaceQuota", err)
	}
	grant, gerr := s.GrantIn(ctx, db, nsID, PrincipalWallet, "0xcreator")
	if gerr != nil || grant.Role != RoleOwner {
		t.Errorf("the previous owner holds %v, %v; want only the owner grant back", grant, gerr)
	}
	var admins int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM grants WHERE role = 'admin' AND revoked_at IS NULL`).Scan(&admins); err != nil || admins != 0 {
		t.Errorf("%d live admin grants, %v; the one written ahead of the move was left behind", admins, err)
	}
}

func TestExplainUnmovedOwner_noRoomIssueMeansTheOwnerWentAway(t *testing.T) {
	s, db, nsID := realRegistry(t)
	giveOwner(t, s, db, nsID, "0xcreator")

	err := s.explainUnmovedOwner(context.Background(), "anchat", nsID, "0xcreator", "0xroomy", 5)
	var quota *ErrNamespaceQuota
	if err == nil || errors.As(err, &quota) {
		t.Fatalf("err = %v, want the namespace-has-no-owner answer", err)
	}
}

func TestTransferOwnership_needsACap(t *testing.T) {
	db := newGrantsDB()
	s := grantsService(t, db)
	seedOwner(t, s, db, "0xowner")

	for _, walletCap := range []int{0, -1} {
		if err := s.TransferOwnership(context.Background(), "anchat", "0xowner", "0xnext", walletCap); err == nil {
			t.Errorf("a transfer with a cap of %d went through", walletCap)
		}
	}
	if owner, err := s.OwnerOf(context.Background(), "anchat"); err != nil || owner != "0xowner" {
		t.Errorf("owner is %q, %v after refused transfers", owner, err)
	}
}

func TestTransferOwnership_aRecipientWithRoomForExactlyOneIsNotRefused(t *testing.T) {
	db := newGrantsDB()
	s := grantsService(t, db)
	seedOwner(t, s, db, "0xowner")
	before := len(db.rows)

	// The recipient owns nothing and the cap is 1: room for exactly this one.
	if err := s.TransferOwnership(context.Background(), "anchat", "0xowner", "0xnext", 1); err != nil {
		t.Fatalf("a wallet owning none was refused at a cap of 1: %v", err)
	}
	if len(db.rows) <= before {
		t.Error("the transfer wrote no grant for the previous owner")
	}
}

// The caller is told only that the transfer was refused; which wallet is at its
// cap, and what the limit is, goes to the audit trail.
func TestTransferOwnership_aRefusalForTheCapIsAuditedWithItsDetail(t *testing.T) {
	s, db, nsID := realRegistry(t)
	giveOwner(t, s, db, nsID, "0xcreator")
	ownNamespaces(t, s, db, "0xfull", 2)
	log, rows := newTestAudit()
	s.audit = log

	err := s.TransferOwnership(context.Background(), "anchat", "0xcreator", "0xFULL", 2)
	var quota *ErrNamespaceQuota
	if !errors.As(err, &quota) {
		t.Fatalf("err = %v, want *ErrNamespaceQuota", err)
	}
	rows.mu.Lock()
	defer rows.mu.Unlock()
	if len(rows.rows) != 1 {
		t.Fatalf("%d audit rows, want the one refusal", len(rows.rows))
	}
	row := rows.rows[0]
	if row[0] != "anchat" || row[1] != "0xcreator" || row[2] != AuditOwnerTransferred || row[3] != "wallet 0xfull" || row[4] != AuditFailure {
		t.Errorf("audit row = %v", row[:5])
	}
	if metadata, _ := row[7].(string); !strings.Contains(metadata, `"limit":"2"`) || !strings.Contains(metadata, "owns as many") {
		t.Errorf("metadata = %v", row[7])
	}
}

func TestTransferOwnership_aTransferThatWorkedIsAuditedAsASuccessOnly(t *testing.T) {
	s, db, nsID := realRegistry(t)
	giveOwner(t, s, db, nsID, "0xcreator")
	log, rows := newTestAudit()
	s.audit = log

	if err := s.TransferOwnership(context.Background(), "anchat", "0xcreator", "0xroomy", 3); err != nil {
		t.Fatal(err)
	}
	rows.mu.Lock()
	defer rows.mu.Unlock()
	var transfers [][]interface{}
	for _, row := range rows.rows {
		if row[2] == AuditOwnerTransferred {
			transfers = append(transfers, row)
		}
	}
	if len(transfers) != 1 || transfers[0][4] != AuditSuccess {
		t.Fatalf("transfer audit rows = %v", transfers)
	}
}
