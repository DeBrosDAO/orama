// Package clientkeytest builds requests as the node's Caddy forwards them, for the tests of code
// that keys or attributes a client by clientkey. It uses only clientkey's exported prover, so a
// request it returns is proven by the same code a gateway runs.
package clientkeytest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/clientkey"
)

// secret is a proof of the length the installer writes (32 bytes in hex).
const secret = "c0ffeec0ffeec0ffeec0ffeec0ffeec0ffeec0ffeec0ffeec0ffeec0ffeec0ff"

// ViaCaddy returns r as the gateway's first middleware leaves a request that carried Caddy's
// proof: the header is gone and the request is marked as proven. The caller sets RemoteAddr
// (loopback) and X-Forwarded-For on r first.
func ViaCaddy(t testing.TB, r *http.Request) *http.Request {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orama-caddy-proof.key")
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatalf("failed to write the test proof: %v", err)
	}
	r.Header.Set(clientkey.ProofHeader, secret)
	var proven *http.Request
	clientkey.NewProver(path, nil).Middleware(http.HandlerFunc(func(_ http.ResponseWriter, got *http.Request) {
		proven = got
	})).ServeHTTP(httptest.NewRecorder(), r)
	if proven == nil {
		t.Fatal("the proof middleware did not call the next handler")
	}
	return proven
}
