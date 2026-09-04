package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// ownershipDB is namespace_ownership and api_keys, enough of them to answer the
// statements the credential paths issue.
//
// The unique index migration 043 adds is modelled too: the insert of a second
// wallet owner fails, the way it does on a real registry. Without that the
// race the guard exists to lose is untestable.
type ownershipDB struct {
	client.DatabaseClient

	walletOwners map[string]string   // namespace id -> wallet
	keyOwners    map[string][]string // namespace id -> hashed keys
	keys         map[string]string   // hashed key -> stored scopes
	walletLinks  map[string]string   // namespace id -> hashed key

	failOwnerRead  bool
	failOwnerWrite bool
	failKeyLink    bool
	// raceWinner, when set, is the wallet that appears as owner the moment an
	// insert is attempted — the other wallet got there first.
	raceWinner string
}

func newOwnershipDB() *ownershipDB {
	return &ownershipDB{
		walletOwners: map[string]string{},
		keyOwners:    map[string][]string{},
		keys:         map[string]string{},
		walletLinks:  map[string]string{},
	}
}

func (d *ownershipDB) Query(_ context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	switch {
	case strings.Contains(sql, "SELECT owner_id FROM namespace_ownership"):
		if d.failOwnerRead {
			return nil, errString("owner read failed")
		}
		ns := key(args[0])
		if d.raceWinner != "" {
			return rows(d.raceWinner), nil
		}
		if owner, ok := d.walletOwners[ns]; ok {
			return rows(owner), nil
		}
		return &client.QueryResult{}, nil

	case strings.Contains(sql, "INSERT INTO namespace_ownership"):
		if d.failOwnerWrite {
			return nil, errString("owner write failed")
		}
		ns := key(args[0])
		if _, taken := d.walletOwners[ns]; taken || d.raceWinner != "" {
			// The partial unique index from migration 043.
			return nil, errString("UNIQUE constraint failed: namespace_ownership.namespace_id")
		}
		d.walletOwners[ns] = getStringVal(args[1])
		return &client.QueryResult{Count: 1}, nil

	case strings.Contains(sql, "INSERT OR IGNORE INTO namespace_ownership"):
		ns := key(args[0])
		d.keyOwners[ns] = append(d.keyOwners[ns], getStringVal(args[1]))
		return &client.QueryResult{Count: 1}, nil

	case strings.Contains(sql, "INSERT OR IGNORE INTO namespaces"),
		strings.Contains(sql, "INSERT INTO namespaces"):
		return &client.QueryResult{Count: 1}, nil

	case strings.Contains(sql, "SELECT id FROM namespaces"):
		return rows(int64(1)), nil

	case strings.Contains(sql, "FROM wallet_api_keys JOIN api_keys"):
		ns := key(args[0])
		if hashed, ok := d.walletLinks[ns]; ok {
			return rows(hashed), nil
		}
		return &client.QueryResult{}, nil

	case strings.Contains(sql, "INSERT INTO api_keys"):
		hashed := getStringVal(args[0])
		d.keys[hashed] = getStringVal(args[3])
		return &client.QueryResult{Count: 1}, nil

	case strings.Contains(sql, "SELECT id FROM api_keys"):
		return rows(int64(7)), nil

	case strings.Contains(sql, "INSERT OR IGNORE INTO wallet_api_keys"):
		if d.failKeyLink {
			return nil, errString("wallet link write failed")
		}
		return &client.QueryResult{Count: 1}, nil
	}
	return nil, errString("unexpected sql: " + sql)
}

func key(v interface{}) string { return getStringVal(v) }

func rows(v interface{}) *client.QueryResult {
	return &client.QueryResult{Count: 1, Rows: [][]interface{}{{v}}}
}

type ownershipNet struct {
	client.NetworkClient
	db *ownershipDB
}

func (n *ownershipNet) Database() client.DatabaseClient { return n.db }

func serviceWith(t *testing.T, db *ownershipDB) *Service {
	t.Helper()
	s := createTestService(t)
	s.orm = &ownershipNet{db: db}
	s.apiKeyORM = nil
	return s
}

func TestClaimNamespaceOwnership_anUnownedNamespaceTakesTheFirstWallet(t *testing.T) {
	db := newOwnershipDB()
	s := serviceWith(t, db)

	if err := s.ClaimNamespaceOwnership(context.Background(), db, int64(1), "anchat", "0xCreator"); err != nil {
		t.Fatalf("the first wallet was refused: %v", err)
	}
	if got := db.walletOwners["1"]; got != "0xcreator" {
		t.Errorf("owner recorded as %q, want the normalized wallet", got)
	}
}

func TestClaimNamespaceOwnership_theOwnerIsAcceptedAgain(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
	s := serviceWith(t, db)

	// Different spelling, same wallet: ownership is normalized, and a login
	// that fails on capitalization would lock an owner out of their own
	// namespace.
	if err := s.ClaimNamespaceOwnership(context.Background(), db, int64(1), "anchat", "0xCREATOR"); err != nil {
		t.Fatalf("the owner was refused on their own namespace: %v", err)
	}
}

// This is the takeover: any wallet that signed a fresh nonce and named an
// existing namespace became a co-owner of it, and got an admin key back.
func TestClaimNamespaceOwnership_aSecondWalletIsRefused(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
	s := serviceWith(t, db)

	err := s.ClaimNamespaceOwnership(context.Background(), db, int64(1), "anchat", "0xsquatter")

	var owned *ErrNamespaceOwnedByAnother
	if !errors.As(err, &owned) {
		t.Fatalf("a second wallet claimed the namespace: %v", err)
	}
	if owned.Namespace != "anchat" {
		t.Errorf("the error names namespace %q, want anchat", owned.Namespace)
	}
	if db.walletOwners["1"] != "0xcreator" {
		t.Errorf("the owner changed to %q", db.walletOwners["1"])
	}
}

// Two wallets arriving together both read no owner. The database decides, and
// the loser has to be told it lost rather than told the gateway broke.
func TestClaimNamespaceOwnership_theLoserOfARaceIsRefusedNotErrored(t *testing.T) {
	db := newOwnershipDB()
	s := serviceWith(t, db)
	db.raceWinner = "0xfaster"

	err := s.ClaimNamespaceOwnership(context.Background(), db, int64(1), "anchat", "0xslower")

	var owned *ErrNamespaceOwnedByAnother
	if !errors.As(err, &owned) {
		t.Fatalf("the loser of the race got %v, want a not-owned error", err)
	}
}

// The same wallet twice concurrently is one wallet, not a conflict.
func TestClaimNamespaceOwnership_losingToYourselfIsSuccess(t *testing.T) {
	db := newOwnershipDB()
	s := serviceWith(t, db)
	db.raceWinner = "0xcreator"

	if err := s.ClaimNamespaceOwnership(context.Background(), db, int64(1), "anchat", "0xCreator"); err != nil {
		t.Fatalf("a wallet that raced itself was refused: %v", err)
	}
}

// A read that fails says so. Treating "cannot tell who owns this" as "nobody
// owns it" is how the guard would be bypassed by a flaky query.
func TestClaimNamespaceOwnership_anUnreadableOwnerIsAnError(t *testing.T) {
	db := newOwnershipDB()
	db.failOwnerRead = true
	s := serviceWith(t, db)

	err := s.ClaimNamespaceOwnership(context.Background(), db, int64(1), "anchat", "0xanyone")
	if err == nil {
		t.Fatal("an unreadable ownership table let the claim through")
	}
	var owned *ErrNamespaceOwnedByAnother
	if errors.As(err, &owned) {
		t.Error("a read failure was reported as someone else's namespace")
	}
}

func TestGetOrCreateAPIKey_refusesANamespaceOwnedByAnother(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
	s := serviceWith(t, db)

	apiKey, err := s.GetOrCreateAPIKey(context.Background(), "0xsquatter", "anchat")

	var owned *ErrNamespaceOwnedByAnother
	if !errors.As(err, &owned) {
		t.Fatalf("got (%q, %v), want a not-owned error and no key", apiKey, err)
	}
	if apiKey != "" {
		t.Errorf("a key was minted anyway: %q", apiKey)
	}
	if len(db.keys) != 0 {
		t.Errorf("%d keys were stored for a refused caller", len(db.keys))
	}
}

// Every minted key carries a grant. GetOrCreateAPIKey wrote no scopes column at
// all, and an empty column was read as admin — so the owner's key was an admin
// key by inference, and so was anyone else's.
func TestGetOrCreateAPIKey_mintsWithAnExplicitGrant(t *testing.T) {
	db := newOwnershipDB()
	s := serviceWith(t, db)

	apiKey, err := s.GetOrCreateAPIKey(context.Background(), "0xcreator", "anchat")
	if err != nil {
		t.Fatalf("the owner was refused on a fresh namespace: %v", err)
	}
	if apiKey == "" {
		t.Fatal("no key was returned")
	}

	if len(db.keys) != 1 {
		t.Fatalf("%d keys stored, want 1", len(db.keys))
	}
	for _, scopes := range db.keys {
		if strings.TrimSpace(scopes) == "" {
			t.Error("the key was stored with no scopes; an empty column denies now, " +
				"so the owner would be locked out of their own namespace")
		}
		if !ScopesFromStored(scopes).IsAdmin() {
			t.Errorf("the owner's key has scopes %q, want admin", scopes)
		}
	}
}

func TestGetOrCreateAPIKey_returnsTheExistingKeyForTheOwner(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
	db.walletLinks["1"] = "already-minted"
	s := serviceWith(t, db)

	apiKey, err := s.GetOrCreateAPIKey(context.Background(), "0xcreator", "anchat")
	if err != nil {
		t.Fatalf("the owner was refused: %v", err)
	}
	if apiKey != "already-minted" {
		t.Errorf("got %q, want the key the wallet already had", apiKey)
	}
	if len(db.keys) != 0 {
		t.Error("a second key was minted for a wallet that already had one")
	}
}

// RequireNamespaceOwner is what a login handler calls before it issues
// anything, so it has to refuse on its own rather than leaning on the check
// inside GetOrCreateAPIKey — by the time that one runs, a JWT and a
// refresh-token row already exist.
func TestRequireNamespaceOwner_refusesANamespaceOwnedByAnother(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
	s := serviceWith(t, db)

	err := s.RequireNamespaceOwner(context.Background(), "0xsquatter", "anchat")

	var owned *ErrNamespaceOwnedByAnother
	if !errors.As(err, &owned) {
		t.Fatalf("RequireNamespaceOwner returned %v, want a not-owned error", err)
	}
}

func TestRequireNamespaceOwner_acceptsTheOwnerAndClaimsAnUnownedNamespace(t *testing.T) {
	db := newOwnershipDB()
	s := serviceWith(t, db)

	if err := s.RequireNamespaceOwner(context.Background(), "0xCreator", "anchat"); err != nil {
		t.Fatalf("the first wallet was refused: %v", err)
	}
	if db.walletOwners["1"] != "0xcreator" {
		t.Errorf("owner recorded as %q", db.walletOwners["1"])
	}
	if err := s.RequireNamespaceOwner(context.Background(), "0xcreator", "anchat"); err != nil {
		t.Errorf("the owner was refused on their second login: %v", err)
	}
}

func TestRequireNamespaceOwner_needsAWallet(t *testing.T) {
	db := newOwnershipDB()
	s := serviceWith(t, db)

	if err := s.RequireNamespaceOwner(context.Background(), "  ", "anchat"); err == nil {
		t.Fatal("an empty wallet claimed a namespace")
	}
}

// A key the caller cannot find again on their next login is a new key every
// time, and the row that links them was written with its error dropped.
func TestGetOrCreateAPIKey_aFailedWalletLinkIsReported(t *testing.T) {
	db := newOwnershipDB()
	db.failKeyLink = true
	s := serviceWith(t, db)

	apiKey, err := s.GetOrCreateAPIKey(context.Background(), "0xcreator", "anchat")
	if err == nil {
		t.Fatalf("a key (%q) was handed back although it was never linked to the wallet", apiKey)
	}
	if apiKey != "" {
		t.Errorf("a key was returned alongside the error: %q", apiKey)
	}
	if !strings.Contains(err.Error(), "anchat") {
		t.Errorf("the error %q does not name the namespace", err)
	}
}
