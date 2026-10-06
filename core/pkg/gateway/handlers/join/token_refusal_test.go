package join

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every refusal used to come back as "invalid or expired token". An operator
// whose install had failed part way through went looking for a clock problem
// when the token had simply been spent — which is exactly the case a retry
// produces, because invites are single-use.

func TestTokenRefusal_saysWhichOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"used", errTokenUsed, "already been used"},
		{"expired", errTokenExpired, "expired"},
		{"unknown", errTokenUnknown, "no invite matches"},
	} {
		got := tokenRefusal(tc.err)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q does not say %q", tc.name, got, tc.want)
		}
	}
}

// The three messages have to differ, or distinguishing them server-side buys
// the operator nothing.
func TestTokenRefusal_messagesAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, err := range []error{errTokenUsed, errTokenExpired, errTokenUnknown} {
		msg := tokenRefusal(err)
		if seen[msg] {
			t.Errorf("two refusals produce the same message: %q", msg)
		}
		seen[msg] = true
	}
}

// A used or expired invite is fixed by minting another, and the message has to
// say so.
func TestTokenRefusal_pointsAtTheFix(t *testing.T) {
	for _, err := range []error{errTokenUsed, errTokenExpired} {
		if got := tokenRefusal(err); !strings.Contains(got, "orama invite") {
			t.Errorf("%q does not say how to get a new invite", got)
		}
	}
}

// Not being able to read the table is an outage, not a verdict about the
// token. Answering 401 would tell the operator to mint a new invite, which
// would fail the same way.
func TestIsTokenRefusal_separatesAVerdictFromAnOutage(t *testing.T) {
	for _, err := range []error{errTokenUsed, errTokenExpired, errTokenUnknown} {
		if !isTokenRefusal(err) {
			t.Errorf("%v is a verdict about the token", err)
		}
	}
	if isTokenRefusal(errors.New("could not read the invite token: connection refused")) {
		t.Error("a database failure is not a verdict about the token")
	}
	if isTokenRefusal(nil) {
		t.Error("no error is not a refusal")
	}
}

// The sentinels have to survive being wrapped with context on the way up.
func TestTokenRefusal_worksThroughWrapping(t *testing.T) {
	wrapped := errors.Join(errors.New("checking the invite"), errTokenUsed)
	if !isTokenRefusal(wrapped) {
		t.Fatal("a wrapped sentinel must still be recognised")
	}
	if !strings.Contains(tokenRefusal(wrapped), "already been used") {
		t.Errorf("got %q", tokenRefusal(wrapped))
	}
}

// End to end through the handler: each refusal reaches the joining node as a
// message it can act on, and a database outage does not.
func TestHandleJoin_reportsWhyTheTokenWasRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mock   *claimQuery
		status int
		want   string
	}{
		{"used", &claimQuery{tokenUsed: true}, http.StatusUnauthorized, "already been used"},
		{"expired", &claimQuery{tokenExpired: true}, http.StatusUnauthorized, "expired"},
		{"unknown", &claimQuery{tokenDead: true}, http.StatusUnauthorized, "no invite matches"},
		{"outage", &claimQuery{tokenErr: errors.New("connection refused")},
			http.StatusServiceUnavailable, "retry shortly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := joinableHandler(t, tc.mock)

			rec := httptest.NewRecorder()
			h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
				bytes.NewReader(joinBody(t, "203.0.113.9"))))

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.status, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("body %q does not say %q", rec.Body.String(), tc.want)
			}
		})
	}
}
