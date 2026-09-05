package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// A credential is accepted six ways and only one of them is the one to keep.
// The others cannot be removed without breaking every client at once, so a
// request that uses one has to be told — on the response, where whoever is
// writing the client sees it.

func TestMarkDeprecatedCredential_saysWhatToDoInstead(t *testing.T) {
	for _, form := range []auth.KeyForm{auth.KeyFormHeader, auth.KeyFormApiKeyScheme, auth.KeyFormBare} {
		rec := httptest.NewRecorder()
		markDeprecatedCredential(rec, form)

		if rec.Header().Get(headerDeprecation) != "true" {
			t.Errorf("%s carries no Deprecation header", form)
		}
		advice := rec.Header().Get(headerDeprecationAdvie)
		if !strings.Contains(advice, "Authorization: Bearer") {
			t.Errorf("%s advice does not say what to send instead: %q", form, advice)
		}
	}
}

// Bearer is the form clients should use, and the WebSocket query token is the
// one a browser leaves no alternative to. Neither may be marked, or every
// correct client is told it is doing something wrong.
func TestMarkDeprecatedCredential_leavesTheSupportedFormsAlone(t *testing.T) {
	for _, form := range []auth.KeyForm{auth.KeyFormBearer, auth.KeyFormQuery, auth.KeyFormNone} {
		rec := httptest.NewRecorder()
		markDeprecatedCredential(rec, form)
		if rec.Header().Get(headerDeprecation) != "" {
			t.Errorf("%s was marked deprecated", form)
		}
	}
}

// One row per namespace per form, not one per request: the audit table is
// replicated to every node, and the fact is the same every time.
func TestDeprecationLog_recordsEachNamespaceAndFormOnce(t *testing.T) {
	log := newDeprecationLog()

	if !log.firstUse("acme", auth.KeyFormHeader) {
		t.Fatal("the first use was not reported as one")
	}
	for i := 0; i < 100; i++ {
		if log.firstUse("acme", auth.KeyFormHeader) {
			t.Fatal("a repeat use was reported as the first")
		}
	}
	if !log.firstUse("acme", auth.KeyFormBare) {
		t.Error("a different form on the same namespace was not reported")
	}
	if !log.firstUse("other", auth.KeyFormHeader) {
		t.Error("the same form on a different namespace was not reported")
	}
}

func TestDeprecationLog_nilIsNotAFirstUse(t *testing.T) {
	var log *deprecationLog
	if log.firstUse("acme", auth.KeyFormHeader) {
		t.Error("a nil log reported a first use, which would record against no gateway")
	}
}

// The forms themselves: which spelling a request used has to be reported
// accurately, or the wrong clients are told to change.
func TestAPIKeyAndFormFromRequest_namesTheSpelling(t *testing.T) {
	cases := []struct {
		name string
		set  func(*http.Request)
		key  string
		form auth.KeyForm
	}{
		{"x-api-key", func(r *http.Request) { r.Header.Set("X-API-Key", "orama_ak_x_1") }, "orama_ak_x_1", auth.KeyFormHeader},
		{"bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer orama_ak_x_1") }, "orama_ak_x_1", auth.KeyFormBearer},
		{"apikey scheme", func(r *http.Request) { r.Header.Set("Authorization", "ApiKey orama_ak_x_1") }, "orama_ak_x_1", auth.KeyFormApiKeyScheme},
		{"no scheme", func(r *http.Request) { r.Header.Set("Authorization", "orama_ak_x_1") }, "orama_ak_x_1", auth.KeyFormBare},
		{"nothing", func(*http.Request) {}, "", auth.KeyFormNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/cache/get", nil)
			tc.set(req)
			key, form := auth.APIKeyAndFormFromRequest(req, false)
			if key != tc.key || form != tc.form {
				t.Errorf("got (%q, %q), want (%q, %q)", key, form, tc.key, tc.form)
			}
		})
	}
}

// A key in a query string is only read on a WebSocket upgrade, and it is the
// one non-Bearer form that is not deprecated: a browser cannot set a header on
// an upgrade.
func TestAPIKeyAndFormFromRequest_theQueryFormIsNotDeprecated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/pubsub/ws?api_key=orama_ak_x_1", nil)
	key, form := auth.APIKeyAndFormFromRequest(req, true)
	if key != "orama_ak_x_1" || form != auth.KeyFormQuery {
		t.Fatalf("got (%q, %q)", key, form)
	}
	if form.Deprecated() {
		t.Error("the WebSocket query token is marked deprecated; a browser has no alternative")
	}
}
