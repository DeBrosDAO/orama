//go:build e2e_fleet

// Package tenancy holds what the tenancy feature packages share: who is
// calling a namespace gateway (its owner, a member of a role, a scoped key, a
// member narrowed by a selector, nobody), which node answers, and how many
// namespaces a package may hold at once.
package tenancy

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// Refusal codes (core/pkg/gateway/auth_errors.go).
const (
	CodeMissing     = "AUTH_MISSING"
	CodeInvalid     = "AUTH_INVALID_KEY"
	CodeRevoked     = "AUTH_REVOKED"
	CodeScope       = "INSUFFICIENT_SCOPE"
	CodeMismatch    = "NAMESPACE_MISMATCH"
	CodeOwnership   = "OWNERSHIP_REQUIRED"
	CodeNotOperator = "NOT_AN_OPERATOR"
)

// Cred is how a request authenticates; the zero value sends nothing.
type Cred struct {
	Bearer string
	APIKey string
}

// Owner is the namespace owner's session.
func Owner(n *ns.Namespace) Cred { return Cred{Bearer: n.Owner.Token()} }

// Send sends body (marshalled, unless it is already []byte) with who's
// credential and fails the test only when the request could not be made.
func Send(t testing.TB, c *gw.Client, method, path string, who Cred, body any) *gw.Response {
	t.Helper()
	req := gw.Req{Method: method, Path: path, Bearer: who.Bearer, APIKey: who.APIKey, Header: http.Header{}}
	if body != nil {
		raw, ok := body.([]byte)
		if !ok {
			var err error
			if raw, err = json.Marshal(body); err != nil {
				t.Fatalf("failed to encode %s body: %v", path, err)
			}
		}
		req.Body = raw
		req.Header.Set("Content-Type", "application/json")
	}
	return c.MustSend(t, req)
}

// Post is Send with POST.
func Post(t testing.TB, c *gw.Client, path string, who Cred, body any) *gw.Response {
	t.Helper()
	return Send(t, c, http.MethodPost, path, who, body)
}

// Get is Send with GET and no body.
func Get(t testing.TB, c *gw.Client, path string, who Cred) *gw.Response {
	t.Helper()
	return Send(t, c, http.MethodGet, path, who, nil)
}

// ExpectRefused fails unless resp is status with code; every 401 and 403
// carries {error, code, hint} (docs/AUTH.md#when-a-request-is-refused).
func ExpectRefused(t testing.TB, resp *gw.Response, status int, code string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(resp.Body, &body); err != nil || resp.Status != status || body["code"] != code {
		t.Fatalf("want HTTP %d %s, got %d: %.300s", status, code, resp.Status, resp.Body)
	}
	if hint, _ := body["hint"].(string); hint == "" {
		t.Errorf("HTTP %d %s carries no hint: %.300s", status, code, resp.Body)
	}
}

// ExpectDenied accepts any 401 or 403. It is for a credential this gateway
// cannot even verify (a session another namespace's gateway signed), where
// which of the two answers is an implementation detail and being served is
// the bug.
func ExpectDenied(t testing.TB, resp *gw.Response, what string) {
	t.Helper()
	if resp.Status != http.StatusUnauthorized && resp.Status != http.StatusForbidden {
		t.Fatalf("%s: want 401/403, got %d: %.300s", what, resp.Status, resp.Body)
	}
}

// Restore sends a request from a t.Cleanup (whose t.Context is already
// cancelled) and reports, without stopping the other cleanups, an answer
// that is none of want.
func Restore(t testing.TB, c *gw.Client, method, path string, who Cred, body any, want ...int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	req := gw.Req{Method: method, Path: path, Bearer: who.Bearer, APIKey: who.APIKey, Header: http.Header{}}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Errorf("cleanup: failed to encode %s body: %v", path, err)
			return
		}
		req.Body = raw
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Send(ctx, req)
	if err != nil || !slices.Contains(want, resp.Status) {
		t.Errorf("cleanup: %s %s did not restore the namespace: %v %v", method, path, err, resp)
	}
}
