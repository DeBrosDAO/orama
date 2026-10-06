package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth/siw"
)

// nonceDB answers what CreateNonce asks: does the namespace exist, and how many
// challenges does this wallet already hold.
type nonceDB struct {
	client.DatabaseClient

	namespaces  map[string]int64
	outstanding int64
	failCount   bool

	inserted int
}

func (d *nonceDB) Query(_ context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	switch {
	case strings.Contains(sql, "SELECT id FROM namespaces"):
		name, _ := args[0].(string)
		if id, ok := d.namespaces[name]; ok {
			return &client.QueryResult{Count: 1, Rows: [][]interface{}{{id}}}, nil
		}
		return &client.QueryResult{}, nil

	case strings.Contains(sql, "SELECT COUNT(*) FROM nonces"):
		if d.failCount {
			return nil, errCount
		}
		return &client.QueryResult{Count: 1, Rows: [][]interface{}{{d.outstanding}}}, nil

	case strings.Contains(sql, "INSERT INTO nonces"):
		d.inserted++
		return &client.QueryResult{Count: 1}, nil

	case strings.Contains(sql, "INSERT OR IGNORE INTO namespaces"),
		strings.Contains(sql, "INSERT INTO namespaces"):
		return nil, errNamespaceCreated
	}
	return nil, errString("unexpected sql: " + sql)
}

const (
	errCount            errString = "count failed"
	errNamespaceCreated errString = "a namespace was created on the challenge path"
)

type nonceNet struct {
	client.NetworkClient
	db *nonceDB
}

func (n *nonceNet) Database() client.DatabaseClient { return n.db }

// testWallet is a real EIP-55 checksummed address: a challenge renders the
// wallet into the message before it writes anything, so a placeholder would
// fail for the wrong reason.
const testWallet = "0xbBbBBBBbbBBBbbbBbbBbbbbBBbBbbbbBbBbbBBbB"

// createChallenge is CreateChallenge with the fields these tests do not vary.
func createChallenge(s *Service, wallet, namespace string) (*Challenge, error) {
	return s.CreateChallenge(context.Background(), ChallengeParams{
		Wallet:    wallet,
		Purpose:   "login",
		Namespace: namespace,
		Chain:     siw.Ethereum,
		Domain:    "gateway.example",
		URI:       "https://gateway.example",
	})
}

func nonceService(t *testing.T, db *nonceDB) *Service {
	t.Helper()
	s := createTestService(t)
	s.orm = &nonceNet{db: db}
	s.apiKeyORM = nil
	return s
}

// The bug: an unauthenticated POST to /v1/auth/challenge ran
// INSERT OR IGNORE INTO namespaces, so squatting a name was free and signing in
// to a name nobody had taken silently created it.
func TestCreateChallenge_refusesAnUnknownNamespace(t *testing.T) {
	db := &nonceDB{namespaces: map[string]int64{"default": 1}}
	s := nonceService(t, db)

	_, err := createChallenge(s, testWallet, "nobody-has-this")

	var unknown *ErrNamespaceUnknown
	if !errors.As(err, &unknown) {
		t.Fatalf("got %v, want a namespace-unknown error", err)
	}
	if unknown.Namespace != "nobody-has-this" {
		t.Errorf("the error names %q", unknown.Namespace)
	}
	if db.inserted != 0 {
		t.Error("a nonce was written for a namespace that does not exist")
	}
}

func TestCreateChallenge_issuesForAnExistingNamespace(t *testing.T) {
	db := &nonceDB{namespaces: map[string]int64{"anchat": 7}}
	s := nonceService(t, db)

	challenge, err := createChallenge(s, testWallet, "anchat")
	if err != nil {
		t.Fatalf("a challenge for an existing namespace was refused: %v", err)
	}
	if challenge.Nonce == "" {
		t.Error("no nonce was returned")
	}
	if challenge.Message == "" {
		t.Error("no message was returned, so there is nothing for a wallet to sign")
	}
	if db.inserted != 1 {
		t.Errorf("%d nonces written, want 1", db.inserted)
	}
}

// A challenge writes a Raft-replicated row for whatever wallet the body names,
// and nothing proves the caller owns it.
func TestCreateChallenge_capsOutstandingChallengesPerWallet(t *testing.T) {
	db := &nonceDB{namespaces: map[string]int64{"anchat": 7}, outstanding: maxOutstandingNonces}
	s := nonceService(t, db)

	_, err := createChallenge(s, testWallet, "anchat")

	var tooMany *ErrTooManyOutstandingNonces
	if !errors.As(err, &tooMany) {
		t.Fatalf("got %v, want a too-many-challenges error", err)
	}
	if db.inserted != 0 {
		t.Error("a nonce was written past the cap")
	}
}

func TestCreateChallenge_allowsUpToTheCap(t *testing.T) {
	db := &nonceDB{namespaces: map[string]int64{"anchat": 7}, outstanding: maxOutstandingNonces - 1}
	s := nonceService(t, db)

	if _, err := createChallenge(s, testWallet, "anchat"); err != nil {
		t.Fatalf("a challenge inside the cap was refused: %v", err)
	}
}

// Not being able to count is not permission to skip the ceiling.
func TestCreateChallenge_refusesWhenTheCountCannotBeRead(t *testing.T) {
	db := &nonceDB{namespaces: map[string]int64{"anchat": 7}, failCount: true}
	s := nonceService(t, db)

	if _, err := createChallenge(s, testWallet, "anchat"); err == nil {
		t.Fatal("a challenge was issued without checking the cap")
	}
	if db.inserted != 0 {
		t.Error("a nonce was written despite an unreadable count")
	}
}

// An empty namespace means the gateway's default, which exists.
func TestCreateChallenge_anEmptyNamespaceUsesTheDefault(t *testing.T) {
	db := &nonceDB{namespaces: map[string]int64{"test-ns": 1}}
	s := nonceService(t, db)

	if _, err := createChallenge(s, testWallet, ""); err != nil {
		t.Fatalf("a namespace-less challenge was refused: %v", err)
	}
}

// The cap only helps if it is a small number. Raising it leaves the code in
// place and the protection gone.
func TestMaxOutstandingNonces_isSmallEnoughToMatter(t *testing.T) {
	if maxOutstandingNonces > 25 {
		t.Errorf("a wallet may hold %d unanswered challenges at once; the point is that "+
			"filling the table for a victim's wallet costs something", maxOutstandingNonces)
	}
	if maxOutstandingNonces < 2 {
		t.Errorf("a cap of %d would break a client that retries a challenge", maxOutstandingNonces)
	}
}
