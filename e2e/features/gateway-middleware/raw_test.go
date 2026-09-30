//go:build e2e_fleet

package gatewaymiddleware

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/edge"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// statusLine matches every HTTP/1.x response status line in a raw exchange.
var statusLine = regexp.MustCompile(`(?m)^HTTP/1\.[01] (\d{3})`)

// rawStatuses are the status codes of every response in raw, in order.
func rawStatuses(raw []byte) []int {
	var out []int
	for _, m := range statusLine.FindAllSubmatch(raw, -1) {
		code, _ := strconv.Atoi(string(m[1]))
		out = append(out, code)
	}
	return out
}

// hugeHeaderBytes is past the 1 MiB request-header limit Go's server (Caddy
// and the gateway both) applies by default.
const hugeHeaderBytes = 2 << 20

// TestRaw_malformedRequestsRefusedNotServed: requests a well-behaved client
// never sends are answered with a 4xx (or the connection closed), never a
// 5xx and never served: a garbage request line, an unknown HTTP version, a
// second Host header, a header over the size limit, a header with a NUL or a
// space before its colon (Caddy terminates HTTP/1.1 in front of every
// gateway; docs/ARCHITECTURE.md "TLS/HTTPS").
func TestRaw_malformedRequestsRefusedNotServed(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	host := f.State.BaseDomain
	cases := map[string]string{
		"garbage request line": "GARBAGE\r\n\r\n",
		"unknown version":      "GET /health HTTP/9.9\r\nHost: " + host + "\r\n\r\n",
		"two Host headers":     "GET /health HTTP/1.1\r\nHost: " + host + "\r\nHost: evil.example\r\nConnection: close\r\n\r\n",
		"no Host":              "GET /health HTTP/1.1\r\nConnection: close\r\n\r\n",
		"space before colon":   "GET /health HTTP/1.1\r\nHost : " + host + "\r\nConnection: close\r\n\r\n",
		"NUL in a header":      "GET /health HTTP/1.1\r\nHost: " + host + "\r\nX-E2E: a\x00b\r\nConnection: close\r\n\r\n",
		"header over 1 MiB":    "GET /health HTTP/1.1\r\nHost: " + host + "\r\nX-E2E: " + strings.Repeat("a", hugeHeaderBytes) + "\r\nConnection: close\r\n\r\n",
		"bad chunk size":       "POST /v1/auth/whoami HTTP/1.1\r\nHost: " + host + "\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\nzz\r\n",
	}
	for what, req := range cases {
		raw, err := harness.GW(t).Raw(t.Context(), []byte(req))
		codes := rawStatuses(raw)
		if err != nil && len(codes) == 0 {
			continue // the connection was closed without an answer: refused
		}
		if len(codes) == 0 || !refusal(codes[0]) {
			t.Errorf("%s: answered %v (%v), want a refusal (4xx, or 505 for a version): %.200q", what, codes, err, raw)
		}
	}
}

// refusal is a status that refuses the request as malformed: a 4xx, or 505
// for a protocol version the server does not speak — not a server fault.
func refusal(code int) bool {
	return (code >= 400 && code < 500) || code == http.StatusHTTPVersionNotSupported
}

// TestRaw_noRequestSmuggling: a body framed two ways (Content-Length and
// chunked) hiding a second request is answered once — the hidden request is
// never served as a request of its own (CL.TE / TE.CL desync). A plain
// request over the same raw path is answered first, so a failing exchange
// cannot pass for a refusal.
func TestRaw_noRequestSmuggling(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	host := f.State.BaseDomain
	plain := "GET /health HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\n\r\n"
	raw, err := harness.GW(t).Raw(t.Context(), []byte(plain))
	if codes := rawStatuses(raw); err != nil || !slices.Equal(codes, []int{http.StatusOK}) {
		t.Fatalf("positive control: a plain GET /health over a raw connection answered %v (%v), want exactly one 200: %.200q", codes, err, raw)
	}
	hidden := "GET /v1/" + edge.RandomLabel(t, "smuggled-") + " HTTP/1.1\r\nHost: " + host + "\r\n\r\n"
	chunked := fmt.Sprintf("0\r\n\r\n%s", hidden)
	cases := map[string]string{
		"CL.TE":         fmt.Sprintf("POST /health HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n%s", host, len(chunked), chunked),
		"TE.CL":         fmt.Sprintf("POST /health HTTP/1.1\r\nHost: %s\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n%x\r\n%s\r\n0\r\n\r\n", host, len(hidden), hidden),
		"TE obfuscated": fmt.Sprintf("POST /health HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\nTransfer-Encoding: xchunked\r\nConnection: close\r\n\r\n%s", host, len(chunked), chunked),
	}
	for what, req := range cases {
		raw, err := harness.GW(t).Raw(t.Context(), []byte(req))
		codes := rawStatuses(raw)
		if err != nil && len(codes) == 0 {
			t.Errorf("%s: the raw exchange failed with no answer, so smuggling is untested: %v", what, err)
			continue
		}
		if len(codes) > 1 {
			t.Errorf("%s: %d responses %v to one request: the hidden request was served", what, len(codes), codes)
		}
		if bytes.Contains(raw, []byte("smuggled-")) {
			t.Errorf("%s: the answer mentions the hidden request's path", what)
		}
	}
}

// TestBodyLimits_oversizedBodiesRefused: handlers read a bounded body; one
// over the bound is refused with a 4xx before any work, and the gateway
// stays up (core/pkg/gateway handlers' MaxBytesReader: namespace creation
// 16 KiB, member grants 4 KiB).
func TestBodyLimits_oversizedBodiesRefused(t *testing.T) {
	t.Parallel()
	f := harness.Fleet(t)
	lobby := newLobbyUser(t, f)
	pad := strings.Repeat("a", 17<<10)
	// The name is invalid on purpose: were the bound missing, validation
	// would refuse it with a different message and nothing is ever created.
	resp := lobby.Client.MustSend(t, jsonReq("/v1/namespaces", lobby.Token(), `{"name":"NOT A NAME","pad":"`+pad+`"}`))
	if resp.Status != http.StatusBadRequest || !strings.Contains(string(resp.Body), "invalid json body") {
		t.Errorf("a 17 KiB namespace creation answered %d %.200s, want 400 invalid json body (the 16 KiB bound)", resp.Status, resp.Body)
	}
	small := lobby.Client.MustSend(t, jsonReq("/v1/namespaces", lobby.Token(), `{"name":"NOT A NAME"}`))
	if small.Status != http.StatusBadRequest || strings.Contains(string(small.Body), "invalid json body") {
		t.Errorf("the same request under the bound answered %d %.200s, want the name refusal", small.Status, small.Body)
	}
	health := harness.GW(t).MustSend(t, jsonReq("/health", "", ""))
	if health.Status != http.StatusOK {
		t.Errorf("/health after the oversized body: %d", health.Status)
	}
}

// newLobbyUser is a fresh wallet signed in to the lobby.
func newLobbyUser(t *testing.T, f *fleet.Fleet) *gw.User {
	t.Helper()
	return gw.NewUser(t, f, gw.LobbyNamespace)
}

// jsonReq is a POST of body with bearer, or a GET when body is empty.
func jsonReq(path, bearer, body string) gw.Req {
	if body == "" {
		return gw.Req{Path: path, Bearer: bearer}
	}
	return gw.Req{Method: http.MethodPost, Path: path, Bearer: bearer, Header: jsonHeader(), Body: []byte(body)}
}
