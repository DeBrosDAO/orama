package hostfunctions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	fetchSecret = "SECRET-QUERY-VALUE"
	fetchHost   = "hidden-destination.example"
	fetchURL    = "https://" + fetchHost + "/private/path?api_key=" + fetchSecret
)

func observedHostFunctions(httpRT, anonRT roundTripperFunc) (*HostFunctions, func() string) {
	core, logs := observer.New(zapcore.DebugLevel)
	h := &HostFunctions{
		logger:         zap.New(core),
		httpClient:     &http.Client{Transport: httpRT},
		anonHTTPClient: &http.Client{Transport: anonRT},
	}
	return h, func() string {
		var out []string
		for _, e := range logs.All() {
			out = append(out, fmt.Sprintf("%s %v", e.Message, e.ContextMap()))
		}
		return strings.Join(out, "\n")
	}
}

func failingRT(msg string) roundTripperFunc {
	return func(*http.Request) (*http.Response, error) { return nil, errors.New(msg) }
}

type brokenBody struct{ msg string }

func (b brokenBody) Read([]byte) (int, error) { return 0, errors.New(b.msg) }

// A function's http_fetch URL can carry a credential in its query string, and
// the client's error quotes the whole URL. The node log keeps the endpoint and
// the cause but not the query.
func TestHTTPFetch_failureLogDropsTheQueryString(t *testing.T) {
	h, logged := observedHostFunctions(failingRT("connection reset by peer"), nil)

	if _, err := h.HTTPFetch(context.Background(), "GET", fetchURL, nil, nil); err != nil {
		t.Fatal(err)
	}
	got := logged()
	if got == "" {
		t.Fatal("nothing was logged, so the test checks nothing")
	}
	for _, leaked := range []string{fetchSecret, "api_key"} {
		if strings.Contains(got, leaked) {
			t.Errorf("the log quotes the query (%q): %s", leaked, got)
		}
	}
	for _, kept := range []string{fetchHost + "/private/path", "connection reset by peer"} {
		if !strings.Contains(got, kept) {
			t.Errorf("the log lost %q: %s", kept, got)
		}
	}
}

func TestHTTPFetch_readFailureLogDropsTheQueryString(t *testing.T) {
	h, logged := observedHostFunctions(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(brokenBody{"body cut"})}, nil
	}, nil)

	if _, err := h.HTTPFetch(context.Background(), "GET", fetchURL, nil, nil); err != nil {
		t.Fatal(err)
	}
	got := logged()
	if got == "" || strings.Contains(got, fetchSecret) || strings.Contains(got, "api_key") {
		t.Fatalf("read failure log: %q", got)
	}
}

// anon_fetch is the route a function takes so the node cannot say where it
// went: neither the URL nor an error naming the destination is logged.
func TestAnonFetch_failureLogNamesNoDestination(t *testing.T) {
	h, logged := observedHostFunctions(nil, failingRT("socks connect tcp 127.0.0.1:9050->"+fetchHost+":443: host unreachable"))

	raw, err := h.AnonFetch(context.Background(), "GET", fetchURL, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"status":0`) {
		t.Fatalf("the function was not told about the failure: %s", raw)
	}
	got := logged()
	if got == "" {
		t.Fatal("nothing was logged, so the test checks nothing")
	}
	for _, leaked := range []string{fetchHost, "/private/path", fetchSecret, "api_key", "9050"} {
		if strings.Contains(got, leaked) {
			t.Errorf("the anon_fetch log names %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "error_class") {
		t.Errorf("the anon_fetch log lost its error class: %s", got)
	}
}
