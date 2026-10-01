package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	pushntfy "github.com/DeBrosOfficial/network/pkg/push/providers/ntfy"
)

const ntfyRelayTestSecret = "a cluster secret"

// fakeLocalNtfy records what the relay publishes to the node's own ntfy.
type fakeLocalNtfy struct {
	mu      sync.Mutex
	path    string
	body    string
	headers http.Header
	calls   int
	status  int
}

func newFakeLocalNtfy(t *testing.T, status int) (*httptest.Server, *fakeLocalNtfy) {
	t.Helper()
	rec := &fakeLocalNtfy{status: status}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.calls++
		rec.path = r.URL.Path
		rec.body = string(b)
		rec.headers = r.Header.Clone()
		rec.mu.Unlock()
		w.WriteHeader(rec.status)
		if rec.status >= 400 {
			_, _ = w.Write([]byte("nope"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func relayGateway(localURL string) *Gateway {
	g := coordinationGateway(ntfyRelayTestSecret)
	g.ntfyLocalURL = localURL
	return g
}

// relayRequest builds a fan-out request as the provider sends it, stamped for
// audience from remote.
func relayRequest(t *testing.T, topic, body, audience, remote string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, pushntfy.FanoutPathPrefix+topic, strings.NewReader(body))
	r.RemoteAddr = remote
	r.Header.Set("Title", "Hi")
	r.Header.Set("Priority", "high")
	r.Header.Set("Tags", "chat")
	r.Header.Set("Authorization", "Bearer should-not-be-relayed")
	r.Header.Set("Actions", "view, Open, https://evil.example")
	key, err := nodeauth.CoordinationKey(ntfyRelayTestSecret)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if err := nodeauth.SignCoordination(key, r, time.Now().Add(testStampLead), audience); err != nil {
		t.Fatalf("sign: %v", err)
	}
	return r
}

func TestHandleInternalNtfyPublish_relaysToLocalNtfy(t *testing.T) {
	srv, rec := newFakeLocalNtfy(t, http.StatusOK)
	g := relayGateway(srv.URL)

	w := httptest.NewRecorder()
	g.handleInternalNtfyPublish(w, relayRequest(t, "up_abc-123", "payload", coordinationTestNode, "10.0.0.7:41000"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (%s); want 200", w.Code, w.Body.String())
	}
	if rec.calls != 1 || rec.path != "/up_abc-123" || rec.body != "payload" {
		t.Errorf("local ntfy got calls=%d path=%q body=%q; want 1 /up_abc-123 payload", rec.calls, rec.path, rec.body)
	}
	for h, want := range map[string]string{"Title": "Hi", "Priority": "high", "Tags": "chat"} {
		if got := rec.headers.Get(h); got != want {
			t.Errorf("relayed %s = %q; want %q", h, got, want)
		}
	}
	for _, h := range []string{"Authorization", "Actions", "X-Orama-Coordination"} {
		if got := rec.headers.Get(h); got != "" {
			t.Errorf("header %s was relayed to ntfy (%q)", h, got)
		}
	}
}

func TestHandleInternalNtfyPublish_refuses(t *testing.T) {
	srv, rec := newFakeLocalNtfy(t, http.StatusOK)
	g := relayGateway(srv.URL)

	cases := map[string]struct {
		req  func(t *testing.T) *http.Request
		code int
	}{
		"a source off the overlay": {
			req: func(t *testing.T) *http.Request {
				return relayRequest(t, "topic1", "x", coordinationTestNode, "203.0.113.9:41000")
			},
			code: http.StatusUnauthorized,
		},
		"a stamp for another node": {
			req: func(t *testing.T) *http.Request {
				return relayRequest(t, "topic1", "x", "12D3KooWOtherNode", "10.0.0.7:41000")
			},
			code: http.StatusUnauthorized,
		},
		"no stamp": {
			req: func(t *testing.T) *http.Request {
				r := httptest.NewRequest(http.MethodPost, pushntfy.FanoutPathPrefix+"topic1", strings.NewReader("x"))
				r.RemoteAddr = "10.0.0.7:41000"
				return r
			},
			code: http.StatusUnauthorized,
		},
		"a body swapped after signing": {
			req: func(t *testing.T) *http.Request {
				r := relayRequest(t, "topic1", "x", coordinationTestNode, "10.0.0.7:41000")
				r.Body = io.NopCloser(strings.NewReader("swapped"))
				return r
			},
			code: http.StatusUnauthorized,
		},
		"a stamp moved onto another topic": {
			req: func(t *testing.T) *http.Request {
				r := relayRequest(t, "topic1", "x", coordinationTestNode, "10.0.0.7:41000")
				r.URL.Path = pushntfy.FanoutPathPrefix + "victim"
				return r
			},
			code: http.StatusUnauthorized,
		},
		"a path past the topic and its sequence ID": {
			req: func(t *testing.T) *http.Request {
				return relayRequest(t, "a/b/c", "x", coordinationTestNode, "10.0.0.7:41000")
			},
			code: http.StatusBadRequest,
		},
		"an empty topic": {
			req: func(t *testing.T) *http.Request {
				return relayRequest(t, "", "x", coordinationTestNode, "10.0.0.7:41000")
			},
			code: http.StatusBadRequest,
		},
		"a topic ntfy would not accept": {
			req: func(t *testing.T) *http.Request {
				return relayRequest(t, "bad%20topic", "x", coordinationTestNode, "10.0.0.7:41000")
			},
			code: http.StatusBadRequest,
		},
		"a topic over 64 characters": {
			req: func(t *testing.T) *http.Request {
				return relayRequest(t, strings.Repeat("a", 65), "x", coordinationTestNode, "10.0.0.7:41000")
			},
			code: http.StatusBadRequest,
		},
		"a GET": {
			req: func(t *testing.T) *http.Request {
				r := httptest.NewRequest(http.MethodGet, pushntfy.FanoutPathPrefix+"topic1", nil)
				r.RemoteAddr = "10.0.0.7:41000"
				return r
			},
			code: http.StatusMethodNotAllowed,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			g.handleInternalNtfyPublish(w, tc.req(t))
			if w.Code != tc.code {
				t.Errorf("status = %d (%s); want %d", w.Code, w.Body.String(), tc.code)
			}
		})
	}
	if rec.calls != 0 {
		t.Errorf("a refused request reached the local ntfy %d times", rec.calls)
	}
}

func TestHandleInternalNtfyPublish_replayedStampRefused(t *testing.T) {
	srv, rec := newFakeLocalNtfy(t, http.StatusOK)
	g := relayGateway(srv.URL)
	r := relayRequest(t, "topic1", "x", coordinationTestNode, "10.0.0.7:41000")
	replay := r.Clone(r.Context())
	replay.Body = io.NopCloser(strings.NewReader("x"))

	w := httptest.NewRecorder()
	g.handleInternalNtfyPublish(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("first publish status = %d; want 200", w.Code)
	}
	w = httptest.NewRecorder()
	g.handleInternalNtfyPublish(w, replay)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("replayed stamp status = %d; want 401", w.Code)
	}
	if rec.calls != 1 {
		t.Errorf("local ntfy published %d times; want 1", rec.calls)
	}
}

func TestHandleInternalNtfyPublish_localNtfyRefuses_isBadGateway(t *testing.T) {
	srv, _ := newFakeLocalNtfy(t, http.StatusForbidden)
	g := relayGateway(srv.URL)

	w := httptest.NewRecorder()
	g.handleInternalNtfyPublish(w, relayRequest(t, "topic1", "x", coordinationTestNode, "10.0.0.7:41000"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502 so the publisher counts this node as failed", w.Code)
	}
	if !strings.Contains(w.Body.String(), "403") {
		t.Errorf("the refusal should say what ntfy answered; got %s", w.Body.String())
	}
}

func TestHandleInternalNtfyPublish_localNtfyDown_isBadGateway(t *testing.T) {
	srv, _ := newFakeLocalNtfy(t, http.StatusOK)
	url := srv.URL
	srv.Close()
	g := relayGateway(url)

	w := httptest.NewRecorder()
	g.handleInternalNtfyPublish(w, relayRequest(t, "topic1", "x", coordinationTestNode, "10.0.0.7:41000"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502", w.Code)
	}
	if !strings.Contains(w.Body.String(), "orama-namespace-ntfy@index") {
		t.Errorf("the error should name the unit to check; got %s", w.Body.String())
	}
}

func TestLocalNtfyURL_defaultIsLoopback(t *testing.T) {
	g := &Gateway{}
	if got := g.localNtfyURL(); got != "http://127.0.0.1:10109" {
		t.Errorf("localNtfyURL = %q; want loopback on the ntfy port", got)
	}
}

func TestFanoutPathPrefix_isADeclaredRoute(t *testing.T) {
	for _, pattern := range gatewayRoutes.Patterns() {
		if pattern == pushntfy.FanoutPathPrefix {
			return
		}
	}
	t.Fatalf("the provider posts to %s, which the gateway does not route", pushntfy.FanoutPathPrefix)
}

// TestHandleInternalNtfyPublish_aSequenceIDIsRelayedToItsTopic: ntfy reads
// POST /<topic>/<sequence-id> as a publish to <topic>; a device token "T/user"
// is that form, and the relay answered 400, so every push to it failed (502).
func TestHandleInternalNtfyPublish_aSequenceIDIsRelayedToItsTopic(t *testing.T) {
	srv, rec := newFakeLocalNtfy(t, http.StatusOK)
	g := relayGateway(srv.URL)
	w := httptest.NewRecorder()
	g.handleInternalNtfyPublish(w, relayRequest(t, "up_abc/user", "payload", coordinationTestNode, "10.0.0.7:41000"))
	if w.Code != http.StatusOK || rec.path != "/up_abc/user" {
		t.Fatalf("status %d path %q; want 200 and /up_abc/user relayed as is", w.Code, rec.path)
	}
}

func TestNtfyTopicPattern_onlyTheTopicAndSequenceForms(t *testing.T) {
	for topic, want := range map[string]bool{
		"up_abc-123":            true,
		"up_abc/user":           true,
		"a/b/c":                 false,
		"/up":                   false,
		"up/":                   false,
		"up/../json":            false,
		"up/x?y":                false,
		"":                      false,
		strings.Repeat("a", 65): false,
	} {
		if got := ntfyTopicPattern.MatchString(topic); got != want {
			t.Errorf("%q: %v, want %v", topic, got, want)
		}
	}
}
