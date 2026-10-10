package gateway

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Phantom browser-session flow is removed.
//
// It was three routes under one public prefix. A session row held the API key
// it minted in cleartext so that an unauthenticated status poll could hand it
// back, and completing a session ran a Solana NFT ownership check against a
// hardcoded mainnet collection over a hardcoded public RPC endpoint — a third
// party deciding, on every login, whether the login was allowed.
//
// Solana wallets sign the same challenge every other wallet signs, through
// /v1/auth/challenge and /v1/auth/verify. Nothing under the old prefix may be
// served again, so this asserts the mux the gateway actually builds answers 404
// for all of it.
func TestPhantomRoutes_areGone(t *testing.T) {
	mux := http.NewServeMux()
	for _, route := range registeredRoutes(t) {
		mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}

	for _, path := range []string{
		"/v1/auth/phantom/session",
		"/v1/auth/phantom/session/0000000000000000000000000000000000000000000000000000000000000000",
		"/v1/auth/phantom/complete",
		"/v1/auth/phantom/",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404. The Phantom flow handed out a minted API key "+
				"from an unauthenticated poll; no route under that prefix comes back.", path, rec.Code)
		}
	}
}

// The routes were the visible half. The other half was phantom_auth_sessions,
// which held every API key the flow minted in cleartext until something read
// it. Migration 049 drops the table, so any code still naming it is querying a
// table that is not there.
func TestPhantomSessionTable_isNotQueriedAnywhere(t *testing.T) {
	root := repoRootFor(t)
	err := filepath.WalkDir(filepath.Join(root, "core"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), "phantom_auth_sessions") {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s names phantom_auth_sessions, which migration 049 drops", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk core: %v", err)
	}
}
