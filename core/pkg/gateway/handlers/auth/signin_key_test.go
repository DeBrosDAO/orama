package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// mintAnswering is a key minter that answers key and err and counts its calls.
func mintAnswering(key string, err error, calls *int) func(context.Context, string, string) (string, error) {
	return func(context.Context, string, string) (string, error) {
		*calls++
		return key, err
	}
}

func TestSignInKey_memberGetsTheMintedKey(t *testing.T) {
	calls := 0
	key, err := signInKey(context.Background(), mintAnswering("ak_x", nil, &calls), "0xa", "acme", "")
	if err != nil || key != "ak_x" || calls != 1 {
		t.Fatalf("key %q err %v calls %d, want the minted key", key, err, calls)
	}
}

// A reader signing in used to be answered 500 ("which holds nothing, so there
// is no key to mint"): the role is documented, and its session is its whole
// credential (stagenet e2e, 2026-09-30).
func TestSignInKey_readerGetsASessionWithoutAKey(t *testing.T) {
	calls := 0
	noKey := fmt.Errorf("%w: role reader", authsvc.ErrNoKeyForRole)
	key, err := signInKey(context.Background(), mintAnswering("", noKey, &calls), "0xa", "acme", "")
	if err != nil || key != "" {
		t.Fatalf("key %q err %v, want no key and no error", key, err)
	}
}

func TestSignInKey_otherMintFailuresAreErrors(t *testing.T) {
	calls := 0
	boom := errors.New("registry unreachable")
	if _, err := signInKey(context.Background(), mintAnswering("", boom, &calls), "0xa", "acme", ""); !errors.Is(err, boom) {
		t.Fatalf("err %v, want the mint failure", err)
	}
}

func TestSignInKey_lobbyAndDeviceSignInsMintNothing(t *testing.T) {
	for name, tc := range map[string]struct{ namespace, device string }{
		"lobby":  {authsvc.LobbyNamespace, ""},
		"device": {"acme", "dev_1"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			key, err := signInKey(context.Background(), mintAnswering("ak_x", nil, &calls), "0xa", tc.namespace, tc.device)
			if err != nil || key != "" || calls != 0 {
				t.Fatalf("key %q err %v calls %d, want nothing minted", key, err, calls)
			}
		})
	}
}

// Asking for a key outright (POST /v1/auth/api-key) as a reader is the
// caller's answer, not a server fault.
func TestWriteCredentialError_noKeyForRoleIsA403WithACode(t *testing.T) {
	rec := httptest.NewRecorder()
	writeCredentialError(rec, "acme", fmt.Errorf("%w: role reader", authsvc.ErrNoKeyForRole))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
	if body := decodeRefusal(t, rec); body["code"] != ErrCodeNoKeyForRole {
		t.Errorf("code %v, want %s", body["code"], ErrCodeNoKeyForRole)
	}
}

// A logout naming nothing is a client mistake: 400, never 500 (stagenet e2e,
// 2026-09-30: `{}` answered 500 "nothing to revoke").
func TestLogoutHandler_nothingNamedIs400(t *testing.T) {
	h := NewHandlers(testLogger(), &authsvc.Service{}, nil, "default", noopInternalAuth)
	for _, body := range []string{`{}`, `{"refresh_token":"   "}`, `{"all":false}`} {
		w := httptest.NewRecorder()
		h.LogoutHandler(w, httptest.NewRequest(http.MethodPost, "/v1/auth/logout", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", body, w.Code)
		}
	}
}
