package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A refresh or a key exchange carries a credential, so its client must not
// follow a redirect off the gateway.
func TestSessionClient_doesNotFollowRedirects(t *testing.T) {
	followed := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true }))
	defer target.Close()
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer gw.Close()

	_, err := exchangeKey(sessionClient(gw.URL), gw.URL, "ak_test")
	if err == nil || followed {
		t.Fatalf("the redirect was followed (%v) or not reported: %v", followed, err)
	}
}
