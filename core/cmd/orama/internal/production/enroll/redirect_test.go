package enroll

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A 307/308 replays the body, and with it the invite token, to wherever the
// Location points, http included (security review, 2026-09-30).
func TestEnrollWithGateway_neverFollowsARedirect(t *testing.T) {
	followed := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true }))
	defer elsewhere.Close()
	gw := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer gw.Close()
	prev := http.DefaultTransport
	http.DefaultTransport = gw.Client().Transport
	defer func() { http.DefaultTransport = prev }()

	err := enrollWithGateway(gw.URL, "secret-token", "code", "203.0.113.10")
	if err == nil || !strings.Contains(err.Error(), "redirected") {
		t.Fatalf("err %v, want the redirect refused", err)
	}
	if followed {
		t.Fatal("the enrollment, with its token, followed the redirect")
	}
}
