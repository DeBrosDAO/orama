package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authsvc "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// A challenge for a namespace that does not exist used to create it. That is
// the caller's answer now, not a server fault, and the message has to say what
// to do — otherwise the flow simply stops working with no explanation.
func TestWriteChallengeError_unknownNamespaceIs404WithACode(t *testing.T) {
	w := httptest.NewRecorder()

	writeChallengeError(w, "myapp", &authsvc.ErrNamespaceUnknown{Namespace: "myapp"})

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != ErrCodeNamespaceUnknown {
		t.Errorf("code %v, want %s", body["code"], ErrCodeNamespaceUnknown)
	}
	if body["namespace"] != "myapp" {
		t.Errorf("namespace %v", body["namespace"])
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "orama namespace create myapp") {
		t.Errorf("the message does not say how to create it: %q", message)
	}
}

func TestWriteChallengeError_tooManyChallengesIs429WithACode(t *testing.T) {
	w := httptest.NewRecorder()

	writeChallengeError(w, "myapp", &authsvc.ErrTooManyOutstandingNonces{Namespace: "myapp", Limit: 10})

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After, so a client has nothing to back off by")
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != ErrCodeTooManyChallenges {
		t.Errorf("code %v, want %s", body["code"], ErrCodeTooManyChallenges)
	}
}

// Anything else really is a server fault. Reporting it as a 404 would tell a
// client the namespace does not exist when the registry was simply unreachable.
func TestWriteChallengeError_otherFailuresStay500(t *testing.T) {
	w := httptest.NewRecorder()

	writeChallengeError(w, "myapp", fmt.Errorf("rqlite unreachable"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := body["code"]; present {
		t.Error("a server fault was given one of the caller-facing codes")
	}
}

// A wallet that is not an address is the caller's mistake (stagenet e2e,
// 2026-09-30: "0x123", "not-a-wallet" and SQL-looking strings answered 500).
func TestWriteChallengeError_refusedMessageIs400(t *testing.T) {
	w := httptest.NewRecorder()
	writeChallengeError(w, "myapp", fmt.Errorf("%w: %q is not an Ethereum address", authsvc.ErrChallengeMessage, "0x123"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

func TestChallengeHandler_oversizedWalletIs400WithoutEchoingIt(t *testing.T) {
	h := NewHandlers(testLogger(), &authsvc.Service{}, nil, "default", noopInternalAuth)
	wallet := strings.Repeat("0x", 4000)
	body := `{"wallet":"` + wallet + `"}`
	w := httptest.NewRecorder()
	h.ChallengeHandler(w, httptest.NewRequest(http.MethodPost, "/v1/auth/challenge", strings.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
	if strings.Contains(w.Body.String(), wallet[:200]) {
		t.Error("the refusal echoes the oversized wallet")
	}
}
