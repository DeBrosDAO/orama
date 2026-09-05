package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
)

// ownershipDB is principals, grants and api_keys — enough of them to answer
// the statements the credential paths issue.
//
// The partial unique index migration 050 carries is modelled too: the insert of
// a second owner grant fails, the way it does on a real registry. Without that
// the race the guard exists to lose is untestable.
type ownershipDB struct {
	client.DatabaseClient

	walletOwners map[string]string   // namespace id -> owning wallet
	keyOwners    map[string][]string // namespace id -> hashed keys with a grant
	keys         map[string]string   // hashed key -> stored scopes
	walletLinks  map[string]string   // namespace id -> hashed key

	// principals is the principals table: an id per (type, identifier), so the
	// grants insert can say which principal it is for the way the real one
	// does — by id, not by name.
	principals map[int64]string
	nextID     int64

	// failGrantRead fails only GrantIn, where failOwnerRead fails both reads.
	// Telling them apart is what makes "an unreadable grant is not the absence
	// of a grant" a thing a test can hold.
	failGrantRead  bool
	failOwnerRead  bool
	failOwnerWrite bool
	failKeyLink    bool
	// raceWinner, when set, is the wallet that appears as owner the moment an
	// insert is attempted — the other wallet got there first. Before the
	// insert it is invisible, which is the whole point: both callers read no
	// owner and both try to write one.
	raceWinner      string
	insertAttempted bool
}

func newOwnershipDB() *ownershipDB {
	return &ownershipDB{
		walletOwners: map[string]string{},
		keyOwners:    map[string][]string{},
		keys:         map[string]string{},
		walletLinks:  map[string]string{},
		principals:   map[int64]string{},
	}
}

func (d *ownershipDB) principalID(identifier string) int64 {
	for id, name := range d.principals {
		if name == identifier {
			return id
		}
	}
	d.nextID++
	d.principals[d.nextID] = identifier
	return d.nextID
}

func (d *ownershipDB) Query(_ context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	switch {
	case strings.Contains(sql, "SELECT g.role, g.resource"):
		// GrantIn: (namespace id, principal type, identifier).
		if d.failOwnerRead || d.failGrantRead {
			return nil, errString("grant read failed")
		}
		ns, ptype, identifier := key(args[0]), getStringVal(args[1]), getStringVal(args[2])
		if ptype == string(PrincipalWallet) && d.walletOwners[ns] == identifier && identifier != "" {
			return &client.QueryResult{Count: 1, Rows: [][]interface{}{
				{"owner", nil, nil, "", "", ""},
			}}, nil
		}
		if ptype == string(PrincipalServiceAccount) {
			for _, held := range d.keyOwners[ns] {
				if held == identifier {
					return &client.QueryResult{Count: 1, Rows: [][]interface{}{
						{"admin", nil, nil, "", "", ""},
					}}, nil
				}
			}
		}
		return &client.QueryResult{}, nil

	case strings.Contains(sql, "SELECT p.identifier FROM grants"):
		if d.failOwnerRead {
			return nil, errString("owner read failed")
		}
		ns := key(args[0])
		if d.raceWinner != "" && d.insertAttempted {
			return rows(d.raceWinner), nil
		}
		if owner, ok := d.walletOwners[ns]; ok {
			return rows(owner), nil
		}
		return &client.QueryResult{}, nil

	case strings.Contains(sql, "INSERT OR IGNORE INTO principals"):
		d.principalID(getStringVal(args[1]))
		return &client.QueryResult{Count: 1}, nil

	case strings.Contains(sql, "SELECT id FROM principals"):
		return rows(d.principalID(getStringVal(args[1]))), nil

	case strings.Contains(sql, "INSERT INTO grants") && strings.Contains(sql, "'owner'"):
		d.insertAttempted = true
		if d.failOwnerWrite {
			return nil, errString("owner write failed")
		}
		ns := key(args[1])
		if _, taken := d.walletOwners[ns]; taken || d.raceWinner != "" {
			// The partial unique index from migration 050.
			return nil, errString("UNIQUE constraint failed: grants.namespace_id")
		}
		d.walletOwners[ns] = d.principals[toInt64(args[0])]
		return &client.QueryResult{Count: 1}, nil

	case strings.Contains(sql, "INSERT OR IGNORE INTO grants"):
		ns := key(args[1])
		d.keyOwners[ns] = append(d.keyOwners[ns], d.principals[toInt64(args[0])])
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

	case strings.Contains(sql, "SELECT api_key_id FROM wallet_api_keys"):
		ns := key(args[0])
		if _, ok := d.walletLinks[ns]; ok {
			return rows(int64(7)), nil
		}
		return &client.QueryResult{}, nil

	case strings.Contains(sql, "INTO wallet_api_keys"):
		d.walletLinks[key(args[0])] = "linked"
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

// Signing in used to claim: the first wallet to reach a namespace with no owner
// became its owner. That is how `default` — created by migration 001 with no
// owner — ended up belonging to whichever wallet happened to sign in first on
// each cluster, and every wallet after it got a 403 on the namespace the docs
// called "where a wallet signs in before it owns anything".
func TestRequireNamespaceMember_doesNotClaim(t *testing.T) {
	db := newOwnershipDB()
	s := serviceWith(t, db)

	err := s.RequireNamespaceMember(context.Background(), "0xCreator", "anchat")
	if !errors.Is(err, ErrNamespaceUnowned) {
		t.Fatalf("an unowned namespace answered %v, want ErrNamespaceUnowned", err)
	}
	if owner := db.walletOwners["1"]; owner != "" {
		t.Errorf("signing in recorded %q as the owner", owner)
	}
}

func TestRequireNamespaceMember_theOwnerIsAcceptedAgain(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
	s := serviceWith(t, db)

	// Different spelling, same wallet: ownership is normalized, and a login
	// that fails on capitalization would lock an owner out of their own
	// namespace.
	if err := s.RequireNamespaceMember(context.Background(), "0xCREATOR", "anchat"); err != nil {
		t.Fatalf("the owner was refused on their own namespace: %v", err)
	}
}

// This is the takeover: any wallet that signed a fresh nonce and named an
// existing namespace became a co-owner of it, and got an admin key back.
func TestRequireNamespaceMember_aSecondWalletIsRefused(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
	s := serviceWith(t, db)

	err := s.RequireNamespaceMember(context.Background(), "0xsquatter", "anchat")

	var owned *ErrNamespaceOwnedByAnother
	if !errors.As(err, &owned) {
		t.Fatalf("a second wallet was let in: %v", err)
	}
	if owned.Namespace != "anchat" {
		t.Errorf("the error names namespace %q, want anchat", owned.Namespace)
	}
	if db.walletOwners["1"] != "0xcreator" {
		t.Errorf("the owner changed to %q", db.walletOwners["1"])
	}
}

// The lobby is where a wallet stands before it owns anything: no grant is
// needed there and none is written, however many wallets arrive.
func TestRequireNamespaceMember_theLobbyIsOpenAndWritesNothing(t *testing.T) {
	db := newOwnershipDB()
	s := serviceWith(t, db)

	for _, wallet := range []string{"0xfirst", "0xsecond"} {
		if err := s.RequireNamespaceMember(context.Background(), wallet, LobbyNamespace); err != nil {
			t.Fatalf("%s was refused the lobby: %v", wallet, err)
		}
	}
	if len(db.walletOwners) != 0 || len(db.principals) != 0 {
		t.Errorf("signing in to the lobby wrote something: owners=%v principals=%v",
			db.walletOwners, db.principals)
	}
}

// A read that fails says so. Treating "cannot tell who owns this" as "nobody
// owns it" is how the guard would be bypassed by a flaky query.
func TestRequireNamespaceMember_anUnreadableGrantIsAnError(t *testing.T) {
	// Only the grant read fails. The owner read still answers, and answers
	// "nobody" — so a caller that treats the failed read as "holds no grant"
	// falls through to the unowned branch and reports the wrong thing about a
	// namespace it could not read.
	db := newOwnershipDB()
	db.failGrantRead = true
	s := serviceWith(t, db)

	err := s.RequireNamespaceMember(context.Background(), "0xanyone", "anchat")
	if err == nil {
		t.Fatal("an unreadable grants table let the caller through")
	}
	if errors.Is(err, ErrNamespaceUnowned) {
		t.Error("a failed grant read was reported as an unowned namespace")
	}
	if !strings.Contains(err.Error(), "read the grants") {
		t.Errorf("the error does not say what could not be read: %v", err)
	}

	t.Run("and an owner read that fails is an error too", func(t *testing.T) {
		db := newOwnershipDB()
		db.failOwnerRead = true
		s := serviceWith(t, db)

		err := s.RequireNamespaceMember(context.Background(), "0xanyone", "anchat")
		if err == nil {
			t.Fatal("an unreadable ownership table let the caller through")
		}
		if errors.Is(err, ErrNamespaceUnowned) {
			t.Error("a read failure was reported as an unowned namespace")
		}
	})
}

func TestGetOrCreateAPIKey_refusesANamespaceOwnedByAnother(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
	s := serviceWith(t, db)

	apiKey, err := s.GetOrCreateAPIKey(context.Background(), "0xsquatter", "anchat")

	if !errors.Is(err, ErrNotAMember) {
		t.Fatalf("got (%q, %v), want a not-a-member error and no key", apiKey, err)
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
	db.walletOwners["1"] = "0xcreator"
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

// Signing in again mints a new key rather than handing back the old one.
//
// It used to SELECT api_keys.key and return that. What is stored is an HMAC of
// the key — production always configures the secret — so a returning owner's
// second login answered with the hash: a string that hashes to something else
// again and is refused everywhere. The raw key is shown once and is not
// recoverable, which is the point of storing a hash, so there is nothing to
// return but a new one.
func TestGetOrCreateAPIKey_mintsAFreshKeyRatherThanReturningTheStoredHash(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
	db.walletLinks["1"] = "already-minted"
	s := serviceWith(t, db)
	s.apiKeyHMACSecret = "secret"

	apiKey, err := s.GetOrCreateAPIKey(context.Background(), "0xcreator", "anchat")
	if err != nil {
		t.Fatalf("the owner was refused: %v", err)
	}
	if apiKey == "already-minted" {
		t.Fatal("the stored value was handed back as the caller's key; it is a hash and authenticates nothing")
	}
	if _, err := ParseKey(apiKey); err != nil {
		t.Errorf("the key returned is not a key: %v", err)
	}
	if _, stored := db.keys[s.HashAPIKey(apiKey)]; !stored {
		t.Error("the key handed to the caller is not the one whose hash was stored, " +
			"so it would be refused on its first use")
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

func TestRequireNamespaceOwner_acceptsTheOwner(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
	s := serviceWith(t, db)

	// Both spellings, twice: a login is a read now, and reading twice changes
	// nothing.
	if err := s.RequireNamespaceOwner(context.Background(), "0xCreator", "anchat"); err != nil {
		t.Fatalf("the owner was refused on their own namespace: %v", err)
	}
	if err := s.RequireNamespaceOwner(context.Background(), "0xcreator", "anchat"); err != nil {
		t.Errorf("the owner was refused on their second login: %v", err)
	}
	if db.walletOwners["1"] != "0xcreator" {
		t.Errorf("owner recorded as %q", db.walletOwners["1"])
	}
}

func TestRequireNamespaceOwner_needsAWallet(t *testing.T) {
	db := newOwnershipDB()
	s := serviceWith(t, db)

	if err := s.RequireNamespaceOwner(context.Background(), "  ", "anchat"); err == nil {
		t.Fatal("an empty wallet was let into a namespace")
	}
}

// A key the caller cannot find again on their next login is a new key every
// time, and the row that links them was written with its error dropped.
func TestGetOrCreateAPIKey_aFailedWalletLinkIsReported(t *testing.T) {
	db := newOwnershipDB()
	db.walletOwners["1"] = "0xcreator"
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
