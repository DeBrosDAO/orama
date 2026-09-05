package deployments

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These endpoints accepted any method. `remove` reads its target from a query
// parameter, so a GET to it deleted the domain — and because nothing enforced
// an answer, the deployment guide and the website documented different verbs.

func TestAllowMethod_accepts_a_listed_method(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/deployments/domains/add", nil)

	if !allowMethod(w, r, http.MethodPost) {
		t.Fatal("POST must be accepted when POST is allowed")
	}
	if w.Code != http.StatusOK {
		t.Errorf("nothing should be written on the accepting path, got %d", w.Code)
	}
}

func TestAllowMethod_rejects_an_unlisted_method(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/deployments/domains/remove", nil)

	if allowMethod(w, r, http.MethodDelete, http.MethodPost) {
		t.Fatal("GET must be rejected when only DELETE and POST are allowed")
	}
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

// A 405 without Allow tells the client it was wrong but not what is right,
// which is how the two docs came to disagree.
func TestAllowMethod_names_what_is_allowed(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/v1/deployments/domains/remove", nil)
	allowMethod(w, r, http.MethodDelete, http.MethodPost)

	allow := w.Header().Get("Allow")
	if !strings.Contains(allow, http.MethodDelete) || !strings.Contains(allow, http.MethodPost) {
		t.Errorf("Allow = %q, want it to name DELETE and POST", allow)
	}
}

func TestAllowMethod_accepts_any_of_several(t *testing.T) {
	for _, method := range []string{http.MethodDelete, http.MethodPost} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/v1/deployments/domains/remove", nil)
		if !allowMethod(w, r, http.MethodDelete, http.MethodPost) {
			t.Errorf("%s must be accepted", method)
		}
	}
}

// Handlers run the guard before anything else, so a wrong method is refused
// without touching the database or the request body.
func TestDomainHandlers_reject_the_wrong_method_before_doing_anything(t *testing.T) {
	h := &DomainHandler{}

	for _, tc := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		wrong   string
	}{
		{"add", h.HandleAddDomain, http.MethodGet},
		{"verify", h.HandleVerifyDomain, http.MethodGet},
		{"list", h.HandleListDomains, http.MethodPost},
		{"remove", h.HandleRemoveDomain, http.MethodGet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			// A nil service and logger would panic on any real work, which is
			// the point: reaching them means the guard did not run first.
			tc.handler(w, httptest.NewRequest(tc.wrong, "/v1/deployments/domains/"+tc.name, nil))

			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s with %s: status = %d, want %d",
					tc.name, tc.wrong, w.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}
