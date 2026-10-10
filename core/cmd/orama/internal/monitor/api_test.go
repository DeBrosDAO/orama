package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

const testToken = "test-token"

// noJitter makes every backoff wait exactly its base.
func noJitter() float64 { return 0.5 }

// testAPISource points an apiSource at handler. Its waits are recorded
// rather than slept, and stopAfter waits cancel the context it returns.
func testAPISource(t *testing.T, handler http.HandlerFunc, stopAfter int) (*apiSource, context.Context, *[]time.Duration) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var waits []time.Duration
	src := &apiSource{
		client: &apiClient{env: "devnet", gatewayURL: srv.URL, http: srv.Client(),
			token: newTokenCache(func() (string, error) { return testToken, nil })},
		jitter: noJitter,
		wait: func(ctx context.Context, d time.Duration) error {
			waits = append(waits, d)
			if len(waits) >= stopAfter {
				cancel()
			}
			return ctx.Err()
		},
	}
	return src, ctx, &waits
}

func snapshotJSON(t *testing.T, hosts ...string) string {
	t.Helper()
	snap := cluster.ClusterSnapshot{CollectedAt: time.Unix(1700000000, 0).UTC()}
	for _, h := range hosts {
		snap.Nodes = append(snap.Nodes, cluster.CollectionStatus{Node: cluster.NodeRef{Host: h}})
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAPISnapshot_happyPath(t *testing.T) {
	body := snapshotJSON(t, "1.1.1.1", "2.2.2.2")
	src, ctx, _ := testAPISource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != telemetryPath || r.Header.Get("Authorization") != "Bearer "+testToken {
			http.Error(w, "wrong request", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, body)
	}, 1)
	snap, err := src.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Nodes) != 2 || snap.Nodes[1].Node.Host != "2.2.2.2" {
		t.Fatalf("got %+v", snap)
	}
}

func TestAPISnapshot_statusErrorsAreActionable(t *testing.T) {
	cases := []struct {
		status   int
		body     string
		wantCode int
		wantText []string
	}{
		{http.StatusUnauthorized, `{"error":"token expired"}`, clierr.CodeAuth, []string{"orama auth login", "token expired", "HTTP 401"}},
		{http.StatusForbidden, `{"error":"not an operator"}`, clierr.CodeAuth, []string{"operators", "HTTP 403"}},
		{http.StatusBadRequest, `{"error":"interval must be between 2 and 60 seconds"}`, clierr.CodeUsage, []string{"HTTP 400", "between 2 and 60"}},
		{http.StatusServiceUnavailable, "starting", clierr.CodeUnavailable, []string{"not ready", "--ssh", "starting"}},
		{http.StatusNotFound, "", clierr.CodeNotFound, []string{"no telemetry API", "--ssh", "HTTP 404"}},
		{http.StatusBadGateway, "<html>bad gateway</html>", clierr.CodeFailure, []string{"HTTP 502", "--ssh"}},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			src, ctx, _ := testAPISource(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}, 1)
			_, err := src.Snapshot(ctx)
			if err == nil {
				t.Fatal("a failed request returned no error")
			}
			if clierr.CodeOf(err) != tc.wantCode {
				t.Fatalf("code = %d, want %d: %v", clierr.CodeOf(err), tc.wantCode, err)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not mention %q: %v", want, err)
				}
			}
		})
	}
}

func TestAPISnapshot_unreachableGatewaySuggestsSSH(t *testing.T) {
	src := &apiSource{client: &apiClient{env: "devnet", gatewayURL: "http://127.0.0.1:1", http: http.DefaultClient,
		token: newTokenCache(func() (string, error) { return testToken, nil })}}
	_, err := src.Snapshot(context.Background())
	if clierr.CodeOf(err) != clierr.CodeUnavailable || !strings.Contains(err.Error(), "--ssh") {
		t.Fatalf("got code %d: %v", clierr.CodeOf(err), err)
	}
}

func TestAPISnapshot_noCredentialsIsAnAuthError(t *testing.T) {
	src := &apiSource{client: &apiClient{env: "devnet", gatewayURL: "http://127.0.0.1:1", http: http.DefaultClient,
		token: newTokenCache(func() (string, error) { return "", errors.New("no credentials found") })}}
	_, err := src.Snapshot(context.Background())
	if clierr.CodeOf(err) != clierr.CodeAuth || !strings.Contains(err.Error(), "orama auth login") {
		t.Fatalf("got code %d: %v", clierr.CodeOf(err), err)
	}
}

func TestAPISnapshot_garbageBody(t *testing.T) {
	src, ctx, _ := testAPISource(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "not json") }, 1)
	if _, err := src.Snapshot(ctx); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("a garbage body was accepted: %v", err)
	}
}

func TestServerMessage_jsonRawAndEmpty(t *testing.T) {
	if got := serverMessage(503, nil); got != "HTTP 503" {
		t.Errorf("empty body: %q", got)
	}
	if got := serverMessage(401, []byte(`{"error":"expired"}`)); got != "HTTP 401: expired" {
		t.Errorf("json body: %q", got)
	}
	long := strings.Repeat("x", maxErrorReasonChars*2)
	if got := serverMessage(500, []byte(long)); len(got) > maxErrorReasonChars+len("HTTP 500: ...") {
		t.Errorf("a long body was not truncated: %d chars", len(got))
	}
}

func TestIntervalSeconds_roundsUp(t *testing.T) {
	for d, want := range map[time.Duration]int{2 * time.Second: 2, 5 * time.Second: 5, 2500 * time.Millisecond: 3} {
		if got := intervalSeconds(d); got != want {
			t.Errorf("intervalSeconds(%s) = %d, want %d", d, got, want)
		}
	}
}

func TestBackoff_doublesToTheCapAndResets(t *testing.T) {
	b := newBackoff(noJitter)
	var got []time.Duration
	for range 7 {
		got = append(got, b.Next())
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, reconnectMax, reconnectMax}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	b.Reset()
	if d := b.Next(); d != reconnectMin {
		t.Fatalf("after Reset: %v, want %v", d, reconnectMin)
	}
}

// The bearer must not follow a redirect: Go would forward it to a tenant
// subdomain or to plain http.
func TestAPIClient_doesNotFollowRedirects(t *testing.T) {
	isolateHome(t)
	c, err := newAPIClientForEnv("unit")
	if err != nil {
		t.Fatal(err)
	}
	if c.gatewayURL != testGatewayURL {
		t.Fatalf("gateway = %s, want the isolated environment's", c.gatewayURL)
	}
	src, ctx, _ := testAPISource(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://app.example.test/steal", http.StatusFound)
	}, 1)
	src.client.http.CheckRedirect = c.http.CheckRedirect
	_, err = src.Snapshot(ctx)
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("a redirect was followed or not reported: %v", err)
	}
}

// A gateway that accepts the connection and never answers must not hang the
// monitor: the transport bounds the wait for headers.
func TestTelemetryHTTPClient_boundsAHungGateway(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	src := &apiSource{client: &apiClient{env: "devnet", gatewayURL: srv.URL,
		http:  newTelemetryHTTPClient(dialTimeout, 100*time.Millisecond),
		token: newTokenCache(func() (string, error) { return testToken, nil })}}

	start := time.Now()
	_, err := src.Snapshot(context.Background())
	if err == nil || clierr.CodeOf(err) != clierr.CodeUnavailable {
		t.Fatalf("got code %d: %v", clierr.CodeOf(err), err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("a hung gateway held the request for %s", time.Since(start))
	}
}

func TestAPISnapshot_unauthorizedDropsTheCachedToken(t *testing.T) {
	fetches := 0
	src, ctx, _ := testAPISource(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "expired", http.StatusUnauthorized)
	}, 1)
	src.client.token = newTokenCache(func() (string, error) { fetches++; return testToken, nil })
	for range 2 {
		if _, err := src.Snapshot(ctx); clierr.CodeOf(err) != clierr.CodeAuth {
			t.Fatalf("got %v", err)
		}
	}
	if fetches != 2 {
		t.Fatalf("fetches = %d, want a new token after the refused one", fetches)
	}
}

// A one-shot that runs out of time is an outage, with the escape hatch named.
func TestAPISnapshot_deadlineIsUnavailable(t *testing.T) {
	release := make(chan struct{})
	src, _, _ := testAPISource(t, func(w http.ResponseWriter, r *http.Request) { <-release }, 1)
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := src.Snapshot(ctx)
	if clierr.CodeOf(err) != clierr.CodeUnavailable || !strings.Contains(err.Error(), "--ssh") {
		t.Fatalf("got code %d: %v", clierr.CodeOf(err), err)
	}
}
