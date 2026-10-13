package namespace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
)

const (
	alice = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bob   = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func listRequest(wallet, namespace string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/namespace/list", nil)
	ctx := context.WithValue(r.Context(), ctxkeys.NamespaceOverride, namespace)
	if wallet != "" {
		ctx = context.WithValue(ctx, ctxkeys.JWT, &auth.JWTClaims{Sub: wallet})
	}
	return r.WithContext(ctx)
}

func listedNames(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Namespaces []struct {
			Name string `json:"name"`
		} `json:"namespaces"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := make([]string, 0, len(body.Namespaces))
	for _, n := range body.Namespaces {
		names = append(names, n.Name)
	}
	return names
}

// The list is the caller's own namespaces, whatever namespace the session is
// in. It used to be those of the current namespace's owner: an admin member
// saw the owner's whole portfolio, and a session in the lobby, which nobody
// owns, got a 500 (stagenet e2e, 2026-09-30).
func TestList_isTheCallersOwnNamespaces(t *testing.T) {
	db := migratedDB(t)
	if _, err := db.Exec(`INSERT INTO cluster_settings (key, value, updated_by) VALUES (?, ?, 'test')`,
		operator.SettingNamespaceCreation, operator.CreationOpen); err != nil {
		t.Fatal(err)
	}
	create := NewCreateHandler(rqlite.NewClient(db), nil, nil, zap.NewNop())
	for wallet, name := range map[string]string{alice: "alice-app", bob: "bob-app"} {
		w := httptest.NewRecorder()
		create.ServeHTTP(w, createRequest(wallet, name))
		if w.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// Alice is an admin of Bob's namespace too; that does not make it hers.
	if _, err := db.Exec(`INSERT INTO grants(principal_id, namespace_id, role, created_by)
		SELECT p.id, n.id, 'admin', 'test' FROM principals p, namespaces n
		 WHERE p.identifier = ? AND n.name = 'bob-app'`, alice); err != nil {
		t.Fatal(err)
	}
	list := NewListHandler(rqlite.NewClient(db), zap.NewNop())

	for _, sessionIn := range []string{"bob-app", "default"} {
		w := httptest.NewRecorder()
		list.ServeHTTP(w, listRequest(alice, sessionIn))
		if w.Code != http.StatusOK {
			t.Fatalf("session in %s: status %d %s", sessionIn, w.Code, w.Body.String())
		}
		if got := listedNames(t, w); len(got) != 1 || got[0] != "alice-app" {
			t.Errorf("session in %s lists %v, want only alice-app", sessionIn, got)
		}
	}
}

func TestList_emptyForAWalletThatOwnsNothing(t *testing.T) {
	list := NewListHandler(rqlite.NewClient(migratedDB(t)), zap.NewNop())
	w := httptest.NewRecorder()
	list.ServeHTTP(w, listRequest(alice, "default"))
	if w.Code != http.StatusOK || len(listedNames(t, w)) != 0 {
		t.Fatalf("status %d, want 200 and an empty list", w.Code)
	}
}

func TestList_requiresASignedInWallet(t *testing.T) {
	list := NewListHandler(rqlite.NewClient(migratedDB(t)), zap.NewNop())
	for name, sub := range map[string]string{"no token": "", "a key subject": "ak_notawallet"} {
		w := httptest.NewRecorder()
		list.ServeHTTP(w, listRequest(sub, "anchat"))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, w.Code)
		}
	}
}
