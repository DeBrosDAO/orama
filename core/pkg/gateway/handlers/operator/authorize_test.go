package operator

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// operatorListDB answers the one query requireOperator asks.
type operatorListDB struct {
	rqlite.Client

	operators map[string]bool
	failQuery bool
	asked     []string

	// writes records every Exec, so a test can see what actually reached the
	// registry rather than what the handler meant to put there.
	writes [][]any
}

func (d *operatorListDB) Exec(_ context.Context, _ string, args ...any) (sql.Result, error) {
	d.writes = append(d.writes, args)
	return execResult{}, nil
}

// execResult is the minimum database/sql result an insert needs.
type execResult struct{}

func (execResult) LastInsertId() (int64, error) { return 1, nil }
func (execResult) RowsAffected() (int64, error) { return 1, nil }

func (d *operatorListDB) Query(_ context.Context, dest any, query string, args ...any) error {
	if d.failQuery {
		return errString("registry unreachable")
	}
	if !strings.Contains(query, "FROM operators") {
		return errString("unexpected query: " + query)
	}
	wallet, _ := args[0].(string)
	d.asked = append(d.asked, wallet)

	rows := reflect.ValueOf(dest).Elem()
	if d.operators[wallet] {
		row := reflect.New(rows.Type().Elem()).Elem()
		row.Field(0).SetString(wallet)
		rows.Set(reflect.Append(rows, row))
	}
	return nil
}

func operatorHandler(t *testing.T, wallets ...string) (*Handler, *operatorListDB) {
	t.Helper()
	db := &operatorListDB{operators: map[string]bool{}}
	for _, w := range wallets {
		db.operators[strings.ToLower(w)] = true
	}
	return NewHandler(zap.NewNop(), db), db
}

func walletRequest(method, path, wallet string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	if wallet != "" {
		r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT, &auth.JWTClaims{Sub: wallet}))
	}
	return r
}

// The bug: /v1/operator/* had no scope entry and no ownership entry, so any
// valid credential could mint a cluster invite — and an invite is handed the
// cluster secret, from which the JWT signing key is derived.
func TestRequireOperator_refusesAWalletThatIsNotAnOperator(t *testing.T) {
	h, _ := operatorHandler(t, "0xoperator")
	w := httptest.NewRecorder()

	_, ok := h.requireOperator(w, walletRequest(http.MethodPost, "/v1/operator/invite", "0xanyone"))

	if ok {
		t.Fatal("a wallet that operates nothing was treated as an operator")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != ErrCodeNotAnOperator {
		t.Errorf("code %v, want %s", body["code"], ErrCodeNotAnOperator)
	}
}

func TestRequireOperator_acceptsAnOperator(t *testing.T) {
	h, _ := operatorHandler(t, "0xoperator")
	w := httptest.NewRecorder()

	wallet, ok := h.requireOperator(w, walletRequest(http.MethodPost, "/v1/operator/invite", "0xoperator"))

	if !ok {
		t.Fatalf("an operator was refused: %d %s", w.Code, w.Body.String())
	}
	if wallet != "0xoperator" {
		t.Errorf("wallet %q", wallet)
	}
}

// The same wallet is written checksummed in one place and lowercase in
// another. An operator locked out by capitalisation would be a worse bug than
// the one this closes.
func TestRequireOperator_isCaseInsensitive(t *testing.T) {
	h, db := operatorHandler(t, "0xoperator")
	w := httptest.NewRecorder()

	if _, ok := h.requireOperator(w, walletRequest(http.MethodPost, "/v1/operator/invite", "0xOPERATOR")); !ok {
		t.Fatalf("an operator was refused because of capitalisation: %d %s", w.Code, w.Body.String())
	}
	if len(db.asked) != 1 || db.asked[0] != "0xoperator" {
		t.Errorf("the list was asked about %v, want the normalised address", db.asked)
	}
}

func TestRequireOperator_refusesWithNoWallet(t *testing.T) {
	h, _ := operatorHandler(t, "0xoperator")
	w := httptest.NewRecorder()

	if _, ok := h.requireOperator(w, walletRequest(http.MethodPost, "/v1/operator/invite", "")); ok {
		t.Fatal("a request with no identity was treated as an operator")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", w.Code)
	}
}

// Not knowing whether someone is an operator is not permission to treat them
// as one. An empty allowlist, or a registry that will not answer, denies.
func TestRequireOperator_deniesWhenTheListCannotBeRead(t *testing.T) {
	h, db := operatorHandler(t)
	db.failQuery = true
	w := httptest.NewRecorder()

	if _, ok := h.requireOperator(w, walletRequest(http.MethodPost, "/v1/operator/invite", "0xanyone")); ok {
		t.Fatal("an unreadable operator list let a caller through")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503 — this is not the caller's fault", w.Code)
	}
}

func TestRequireOperator_anEmptyListDenies(t *testing.T) {
	h, _ := operatorHandler(t)
	w := httptest.NewRecorder()

	if _, ok := h.requireOperator(w, walletRequest(http.MethodPost, "/v1/operator/invite", "0xanyone")); ok {
		t.Fatal("a cluster with no operators treated everyone as one")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", w.Code)
	}
}

// Every operator endpoint has to be gated, not just the one that mints
// invites: listing nodes is an inventory of the cluster, and registering one
// claims it.
func TestEveryOperatorEndpointRequiresAnOperator(t *testing.T) {
	for name, call := range map[string]func(*Handler, http.ResponseWriter, *http.Request){
		"invite":   (*Handler).HandleInvite,
		"nodes":    (*Handler).HandleListNodes,
		"register": (*Handler).HandleRegister,
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := operatorHandler(t, "0xoperator")
			w := httptest.NewRecorder()

			method := http.MethodPost
			if name == "nodes" {
				method = http.MethodGet
			}
			call(h, w, walletRequest(method, "/v1/operator/"+name, "0xanyone"))

			if w.Code != http.StatusForbidden {
				t.Errorf("%s answered %d to a non-operator, want 403: %s",
					name, w.Code, strings.TrimSpace(w.Body.String()))
			}
		})
	}
}

// An invite token in the registry is a key to every secret the cluster holds.
// What is stored has to be a hash of it and nothing else.
func TestHashInviteToken(t *testing.T) {
	const token = "0123456789abcdef"

	hashed := HashInviteToken(token)

	if hashed == token {
		t.Fatal("the token was stored as itself")
	}
	if strings.Contains(hashed, token) {
		t.Fatal("the stored value contains the token")
	}
	if !strings.HasPrefix(hashed, inviteTokenHashPrefix) {
		t.Errorf("hash %q has no prefix; migration 044 tells a converted row from a "+
			"plaintext one by it, and would delete every row on a re-apply without it", hashed)
	}
	if again := HashInviteToken(token); again != hashed {
		t.Error("hashing is not deterministic, so a token could never be looked up")
	}
	if HashInviteToken("another") == hashed {
		t.Error("two tokens hash the same")
	}
	// The gateway looks up what the operator pasted, which may carry
	// whitespace from a copy.
	if HashInviteToken("  "+token+"\n") != hashed {
		t.Error("surrounding whitespace changes the hash, so a pasted token would not be found")
	}
}

// The registry must never hold a usable invite token. The column was the raw
// token, as its primary key, so a disk snapshot or a raw rqlite query was a
// credential for the cluster secret, the swarm key and everything else
// /v1/internal/join hands out.
func TestHandleInvite_storesOnlyAHash(t *testing.T) {
	h, db := operatorHandler(t, "0xoperator")
	w := httptest.NewRecorder()

	h.HandleInvite(w, walletRequest(http.MethodPost, "/v1/operator/invite", "0xoperator"))

	if w.Code != http.StatusOK {
		t.Fatalf("minting failed: %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	}

	var body InviteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Token == "" {
		t.Fatal("no token was returned; the operator has nothing to use")
	}

	if len(db.writes) != 1 {
		t.Fatalf("%d writes reached the registry, want 1", len(db.writes))
	}
	stored, _ := db.writes[0][0].(string)
	if stored == body.Token {
		t.Fatal("the token itself was written to the registry")
	}
	if stored != HashInviteToken(body.Token) {
		t.Errorf("stored %q, want the hash of the returned token — the gateway hashes "+
			"what a joining node presents, so anything else can never be looked up", stored)
	}
}

// An invite is a credential for every secret the cluster holds. A week is
// longer than any reason to mint one.
func TestHandleInvite_capsTheLifetime(t *testing.T) {
	h, _ := operatorHandler(t, "0xoperator")
	w := httptest.NewRecorder()

	r := httptest.NewRequest(http.MethodPost, "/v1/operator/invite",
		strings.NewReader(`{"expiry_minutes": 10080}`))
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.JWT, &auth.JWTClaims{Sub: "0xoperator"}))
	h.HandleInvite(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("minting failed: %d %s", w.Code, strings.TrimSpace(w.Body.String()))
	}

	var body InviteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	expires, err := time.Parse("2006-01-02 15:04:05", body.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expiry %q: %v", body.ExpiresAt, err)
	}
	if until := time.Until(expires); until > time.Hour+time.Minute {
		t.Errorf("the invite lives %s; a week was long enough to outlive the reason "+
			"it was minted", until.Round(time.Minute))
	}
}
