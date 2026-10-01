package ntfy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push"
)

func TestSend_happy_path(t *testing.T) {
	var (
		gotPath     string
		gotBody     string
		gotTitle    string
		gotPriority string
		gotAuth     string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTitle = r.Header.Get("Title")
		gotPriority = r.Header.Get("Priority")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL, AuthToken: "secret"}, nil)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "ns/myapp/user-1",
		Title:       "Hello",
		Body:        "World",
		Priority:    push.PriorityHigh,
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if gotPath != "/ns/myapp/user-1" {
		t.Errorf("expected path /ns/myapp/user-1, got %s", gotPath)
	}
	if gotTitle != "Hello" {
		t.Errorf("expected Title=Hello, got %s", gotTitle)
	}
	if gotPriority != "high" {
		t.Errorf("expected Priority=high, got %s", gotPriority)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("expected Authorization=Bearer secret, got %s", gotAuth)
	}
	if gotBody != "World" {
		t.Errorf("expected body=World, got %s", gotBody)
	}
}

// Bugboard #126: ntfy does not relay X-* headers to subscribers, so Data must
// ride the body. With no explicit Body, a data-only push serializes Data as
// the JSON body — and must NOT set the dead X-Data header.
func TestSend_dataOnly_ridesBody_noXDataHeader(t *testing.T) {
	var gotBody, gotData string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotData = r.Header.Get("X-Data")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "topic",
		Data:        map[string]interface{}{"call_id": "abc-123"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotData != "" {
		t.Errorf("X-Data header must not be set (ntfy drops it); got %q", gotData)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(gotBody), &got); err != nil {
		t.Fatalf("data-only body not valid JSON: %v (body=%q)", err, gotBody)
	}
	if got["call_id"] != "abc-123" {
		t.Errorf("data did not ride the body: got %v", got)
	}
}

// An explicit Body wins — Data does NOT clobber a caller-supplied body (the
// caller owns the envelope; this is anchat's call-push pattern).
func TestSend_explicitBody_winsOverData(t *testing.T) {
	var gotBody, gotData string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotData = r.Header.Get("X-Data")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "topic",
		Body:        `{"type":"call.invite","callId":"c1"}`,
		Data:        map[string]interface{}{"ignored": "yes"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotBody != `{"type":"call.invite","callId":"c1"}` {
		t.Errorf("explicit body not preserved; got %q", gotBody)
	}
	if gotData != "" {
		t.Errorf("X-Data header must not be set; got %q", gotData)
	}
}

func TestSend_no_auth_header_when_token_empty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	if err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
}

func TestSend_4xx_returns_error_with_body_excerpt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden topic"))
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"})
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("error should mention status and body, got: %v", err)
	}
}

func TestSend_empty_token_returns_ErrEmptyToken(t *testing.T) {
	p := New(Config{BaseURL: "http://example.invalid"}, nil)
	err := p.Send(context.Background(), push.PushMessage{Body: "x"})
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if err != push.ErrEmptyToken {
		t.Errorf("expected ErrEmptyToken, got %v", err)
	}
}

func TestSend_short_timeout_returns_error(t *testing.T) {
	// Server that blocks for 2s — provider with 100ms timeout should give up.
	blockUntil := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blockUntil:
		case <-time.After(2 * time.Second):
		}
	}))
	defer func() { close(blockUntil); srv.Close() }()

	p := New(Config{BaseURL: srv.URL, Timeout: 100 * time.Millisecond}, nil)
	start := time.Now()
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"})
	elapsed := time.Since(start)
	if err == nil {
		t.Error("expected timeout error")
	}
	if elapsed > 1*time.Second {
		t.Errorf("expected fast timeout, took %v", elapsed)
	}
}

func TestSend_no_baseURL_returns_error(t *testing.T) {
	p := New(Config{}, nil)
	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"})
	if err == nil {
		t.Fatal("expected error for missing base URL")
	}
}

// feat-32: an Android/GrapheneOS UnifiedPush device registers the full endpoint
// URL its distributor hands it. UnifiedPush requires the app server to POST to
// that endpoint verbatim, and we must do so ONLY when the host matches our
// configured push server (never an arbitrary host → no SSRF).

func TestSend_unifiedPush_endpoint_published(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	// The distributor hands the client a full endpoint on the SAME (push) host.
	endpoint := srv.URL + "/upAbc123"
	if err := p.Send(context.Background(), push.PushMessage{DeviceToken: endpoint, Body: "payload"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/upAbc123" {
		t.Errorf("UnifiedPush endpoint must publish to its topic path; got %q", gotPath)
	}
	if gotBody != "payload" {
		t.Errorf("body not delivered; got %q", gotBody)
	}
}

func TestSend_unifiedPush_endpoint_confined_to_topic(t *testing.T) {
	// A URL token must be confined to the same publish surface as a bare topic:
	// the path becomes the topic, and any query string is dropped — so it can't
	// gain arbitrary path/query control on the push host.
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	endpoint := srv.URL + "/uptopic?admin=1&x=y"
	if err := p.Send(context.Background(), push.PushMessage{DeviceToken: endpoint, Body: "x"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/uptopic" {
		t.Errorf("path must be the topic only; got %q", gotPath)
	}
	if gotQuery != "" {
		t.Errorf("query string must be dropped (no arbitrary query on push host); got %q", gotQuery)
	}
}

func TestSend_unifiedPush_endpoint_rejects_userinfo_bypass(t *testing.T) {
	// Classic SSRF guard bypass: smuggle the real host into userinfo. url.Parse
	// resolves the authority to the attacker host, so it must be rejected.
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// base host = srv host; token tries "<srvhost>@attacker.example.com".
	base, _ := url.Parse(srv.URL)
	p := New(Config{BaseURL: srv.URL}, nil)
	token := base.Scheme + "://" + base.Host + "@attacker.example.com/x"
	if err := p.Send(context.Background(), push.PushMessage{DeviceToken: token, Body: "x"}); err == nil {
		t.Fatal("expected rejection of a userinfo-smuggled host")
	}
	if hit {
		t.Error("no request must be sent for a userinfo-bypass token")
	}
}

func TestSend_unifiedPush_endpoint_rejects_foreign_host(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(Config{BaseURL: srv.URL}, nil)
	// A device token pointing at a DIFFERENT host must be rejected before any
	// request is made — a device token must never become an SSRF vector.
	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "https://attacker.example.com/steal",
		Body:        "x",
	})
	if err == nil {
		t.Fatal("expected an error for an endpoint whose host doesn't match the push host")
	}
	if hit {
		t.Error("no request must be sent when the endpoint host doesn't match")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("error should explain the host mismatch; got %v", err)
	}
}

func TestName(t *testing.T) {
	p := New(Config{BaseURL: "http://x"}, nil)
	if p.Name() != "ntfy" {
		t.Errorf("expected Name=ntfy, got %s", p.Name())
	}
}

// ----------------------------------------------------------------------------
// Bugboard #858 — cluster fan-out. Each push node runs an independent ntfy with
// no shared store, so a publish must reach EVERY node for the subscriber's
// instance (round-robin DNS picks one) to receive it. The fan-out travels the
// overlay to each node's internal gateway, signed for that node.
// ----------------------------------------------------------------------------

// fanoutRecorder is a test node internal gateway that records what it received.
type fanoutRecorder struct {
	mu      sync.Mutex
	paths   []string
	bodies  []string
	headers []http.Header
}

func newFanoutNode(t *testing.T) (*httptest.Server, *fanoutRecorder) {
	t.Helper()
	rec := &fanoutRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.paths = append(rec.paths, r.URL.Path)
		rec.bodies = append(rec.bodies, string(b))
		rec.headers = append(rec.headers, r.Header.Clone())
		rec.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return srv, rec
}

func (r *fanoutRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.paths)
}

// stampSigner records the node each request was signed for and marks the
// request so a test can tell it was signed.
func stampSigner(mu *sync.Mutex, audiences map[string]string) func(*http.Request, string) error {
	return func(req *http.Request, nodeID string) error {
		req.Header.Set("X-Test-Stamp", nodeID)
		mu.Lock()
		audiences[req.URL.Host] = nodeID
		mu.Unlock()
		return nil
	}
}

func targetsOf(servers map[string]*httptest.Server) func(context.Context) ([]FanoutTarget, error) {
	return func(context.Context) ([]FanoutTarget, error) {
		out := make([]FanoutTarget, 0, len(servers))
		for id, s := range servers {
			out = append(out, FanoutTarget{NodeID: id, BaseURL: s.URL})
		}
		return out, nil
	}
}

func TestSend_fanout_publishesToAllNodesSigned(t *testing.T) {
	s1, r1 := newFanoutNode(t)
	defer s1.Close()
	s2, r2 := newFanoutNode(t)
	defer s2.Close()
	s3, r3 := newFanoutNode(t)
	defer s3.Close()
	var mu sync.Mutex
	audiences := map[string]string{}

	p := New(Config{
		BaseURL:        "https://push.example.com", // never dialled when fanning out
		AuthToken:      "tok",
		FanoutResolver: targetsOf(map[string]*httptest.Server{"peer-1": s1, "peer-2": s2, "peer-3": s3}),
		FanoutSigner:   stampSigner(&mu, audiences),
	}, nil)

	err := p.Send(context.Background(), push.PushMessage{
		DeviceToken: "user-1", Body: "hi", Title: "T", Priority: push.PriorityHigh, Channel: "chat",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	for i, tc := range []struct {
		rec  *fanoutRecorder
		peer string
	}{{r1, "peer-1"}, {r2, "peer-2"}, {r3, "peer-3"}} {
		if tc.rec.count() != 1 {
			t.Fatalf("node %d received %d publishes; want exactly 1", i+1, tc.rec.count())
		}
		if got := tc.rec.paths[0]; got != FanoutPathPrefix+"user-1" {
			t.Errorf("node %d path = %q; want the internal fan-out route", i+1, got)
		}
		if tc.rec.bodies[0] != "hi" {
			t.Errorf("node %d body = %q; want hi", i+1, tc.rec.bodies[0])
		}
		h := tc.rec.headers[0]
		if h.Get("X-Test-Stamp") != tc.peer {
			t.Errorf("node %d request was signed for %q; want %q", i+1, h.Get("X-Test-Stamp"), tc.peer)
		}
		if h.Get("Title") != "T" || h.Get("Priority") != "high" || h.Get("Tags") != "chat" {
			t.Errorf("node %d headers = %v; want Title/Priority/Tags carried", i+1, h)
		}
		if h.Get("Authorization") != "" {
			t.Errorf("node %d got the ntfy bearer token on an internal hop", i+1)
		}
	}
}

func TestSend_fanout_oneNodeDown_stillSucceeds(t *testing.T) {
	up, rUp := newFanoutNode(t)
	defer up.Close()
	down, _ := newFanoutNode(t)
	down.Close() // unreachable
	var mu sync.Mutex

	p := New(Config{
		BaseURL:        up.URL,
		FanoutResolver: targetsOf(map[string]*httptest.Server{"up": up, "down": down}),
		FanoutSigner:   stampSigner(&mu, map[string]string{}),
	}, nil)

	if err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"}); err != nil {
		t.Fatalf("Send should succeed when at least one node is up; got %v", err)
	}
	if rUp.count() != 1 {
		t.Errorf("the up node should have received the publish; got %d", rUp.count())
	}
}

func TestSend_fanout_allNodesDown_returnsError(t *testing.T) {
	d1, _ := newFanoutNode(t)
	d1.Close()
	d2, _ := newFanoutNode(t)
	d2.Close()
	var mu sync.Mutex

	p := New(Config{
		BaseURL:        "http://127.0.0.1:1",
		FanoutResolver: targetsOf(map[string]*httptest.Server{"a": d1, "b": d2}),
		FanoutSigner:   stampSigner(&mu, map[string]string{}),
	}, nil)

	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"})
	if err == nil {
		t.Fatal("Send should fail when every node is unreachable")
	}
	if !strings.Contains(err.Error(), "all 2 push nodes failed") {
		t.Errorf("error should say every node failed; got %v", err)
	}
}

func TestSend_fanout_everyNodeRefuses_returnsError(t *testing.T) {
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer refuse.Close()
	var mu sync.Mutex

	p := New(Config{
		BaseURL:        "http://127.0.0.1:1",
		FanoutResolver: targetsOf(map[string]*httptest.Server{"a": refuse}),
		FanoutSigner:   stampSigner(&mu, map[string]string{}),
	}, nil)

	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a node refusing the publish must fail the Send with its status; got %v", err)
	}
}

func TestSend_fanout_noNodes_returnsErrorWithoutPublishingElsewhere(t *testing.T) {
	base, rBase := newFanoutNode(t)
	defer base.Close()
	var mu sync.Mutex

	p := New(Config{
		BaseURL:        base.URL,
		FanoutResolver: func(context.Context) ([]FanoutTarget, error) { return nil, nil },
		FanoutSigner:   stampSigner(&mu, map[string]string{}),
	}, nil)

	if err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"}); err == nil {
		t.Fatal("Send with no push nodes must fail, not pretend it published")
	}
	if rBase.count() != 0 {
		t.Errorf("an empty node set must not fall back to the base URL; base got %d publishes", rBase.count())
	}
}

func TestSend_fanout_resolverError_returnsErrorWithoutPublishingElsewhere(t *testing.T) {
	base, rBase := newFanoutNode(t)
	defer base.Close()
	var mu sync.Mutex

	p := New(Config{
		BaseURL:        base.URL,
		FanoutResolver: func(context.Context) ([]FanoutTarget, error) { return nil, context.DeadlineExceeded },
		FanoutSigner:   stampSigner(&mu, map[string]string{}),
	}, nil)

	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a resolver failure must surface; got %v", err)
	}
	if rBase.count() != 0 {
		t.Errorf("a resolver failure must not fall back to the base URL; base got %d publishes", rBase.count())
	}
}

func TestSend_fanout_signerError_failsEveryNode(t *testing.T) {
	s, rec := newFanoutNode(t)
	defer s.Close()

	p := New(Config{
		BaseURL:        s.URL,
		FanoutResolver: targetsOf(map[string]*httptest.Server{"a": s}),
		FanoutSigner:   func(*http.Request, string) error { return errors.New("no cluster secret") },
	}, nil)

	err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"})
	if err == nil || !strings.Contains(err.Error(), "no cluster secret") {
		t.Fatalf("a signing failure must surface; got %v", err)
	}
	if rec.count() != 0 {
		t.Errorf("an unsigned request was sent to a node (%d)", rec.count())
	}
}

func TestSend_fanout_withoutSigner_returnsError(t *testing.T) {
	s, rec := newFanoutNode(t)
	defer s.Close()

	p := New(Config{
		BaseURL:        s.URL,
		FanoutResolver: targetsOf(map[string]*httptest.Server{"a": s}),
	}, nil)

	if err := p.Send(context.Background(), push.PushMessage{DeviceToken: "t", Body: "x"}); err == nil {
		t.Fatal("fan-out without a signer must fail")
	}
	if rec.count() != 0 {
		t.Errorf("an unsigned request was sent to a node (%d)", rec.count())
	}
}
