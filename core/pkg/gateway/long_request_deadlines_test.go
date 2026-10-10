package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// deadlineServer serves longRequestDeadlines behind the logging middleware's
// writer wrapper, as the chain does, with a 200ms ReadTimeout standing in for
// the gateway's 60s. authenticate marks the request as auth would.
func deadlineServer(t *testing.T, authenticate func(*http.Request) *http.Request) *httptest.Server {
	t.Helper()
	read := longRequestDeadlines(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusRequestTimeout)
		}
	}))
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		read.ServeHTTP(&statusResponseWriter{ResponseWriter: w, status: http.StatusOK}, authenticate(r))
	}))
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func withAPIKey(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKeyAPIKey, "ak_test"))
}

// A namespace gateway serves the upload itself: the chain gives an
// authenticated caller the long budget, not only the proxy in front of it.
func TestLongRequestDeadlines_anAuthenticatedSlowUploadIsReadInFull(t *testing.T) {
	srv := deadlineServer(t, withAPIKey)
	if status, err := slowPost(t, srv.URL+"/v1/storage/upload", 600*time.Millisecond, "head"); err != nil || status != http.StatusOK {
		t.Fatalf("a slow upload was cut off (%d, %v)", status, err)
	}
	if status, err := slowPost(t, srv.URL+"/v1/cache/put", 600*time.Millisecond, "head"); err == nil && status == http.StatusOK {
		t.Fatal("a short route read a body slower than the server's timeout; it must keep the server's timeouts")
	}
}

// An anonymous call to a public function must not hold a connection longer
// than the server allows.
func TestLongRequestDeadlines_anAnonymousCallKeepsTheServersTimeouts(t *testing.T) {
	srv := deadlineServer(t, func(r *http.Request) *http.Request { return r })
	if status, err := slowPost(t, srv.URL+"/v1/invoke/acme/fn", 600*time.Millisecond, "head"); err == nil && status == http.StatusOK {
		t.Fatal("an anonymous slow body was read in full; it must keep the server's timeouts")
	}
}

func TestCallerAuthenticated(t *testing.T) {
	plain := func() *http.Request { return httptest.NewRequest(http.MethodPost, "/v1/storage/upload", nil) }
	vouched := plain()
	vouched.Header.Set(HeaderInternalAuthValidated, "true")
	vouched.Header.Set(HeaderInternalAuthNamespace, "acme")
	noNamespace := plain()
	noNamespace.Header.Set(HeaderInternalAuthValidated, "true")
	jwt := plain()
	jwt = jwt.WithContext(context.WithValue(jwt.Context(), ctxKeyJWT, &auth.JWTClaims{Sub: "0xabc"}))
	for name, tc := range map[string]struct {
		r    *http.Request
		want bool
	}{
		"anonymous":                     {plain(), false},
		"api key":                       {withAPIKey(plain()), true},
		"jwt":                           {jwt, true},
		"vouched for by a gateway":      {vouched, true},
		"vouched for with no namespace": {noNamespace, false},
	} {
		if got := callerAuthenticated(tc.r); got != tc.want {
			t.Errorf("%s: callerAuthenticated = %v, want %v", name, got, tc.want)
		}
	}
}
