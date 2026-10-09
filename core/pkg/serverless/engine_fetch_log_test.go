package serverless

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const (
	engineFetchSecret = "SECRET-QUERY-VALUE"
	engineFetchHost   = "hidden-destination.example"
	engineFetchURL    = "https://" + engineFetchHost + "/private/path?api_key=" + engineFetchSecret
)

// failingFetchServices fails both fetch host functions with the error an HTTP
// client gives: a *url.Error that quotes the whole URL.
type failingFetchServices struct {
	*MockHostServices
}

func (failingFetchServices) failure(rawURL string) error {
	return &url.Error{Op: "Get", URL: rawURL, Err: errors.New("dial through " + engineFetchHost + ": connection reset")}
}

func (f failingFetchServices) HTTPFetch(_ context.Context, _, u string, _ map[string]string, _ []byte) ([]byte, error) {
	return nil, f.failure(u)
}

func (f failingFetchServices) AnonFetch(_ context.Context, _, u string, _ map[string]string, _ []byte) ([]byte, error) {
	return nil, f.failure(u)
}

// guestMemory is the smallest WASM module there is: one exported page of
// memory, to read the host function arguments from.
var guestMemory = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic, version
	0x05, 0x03, 0x01, 0x00, 0x01, // memory section: one memory of one page
	0x07, 0x0a, 0x01, 0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00, // export "memory"
}

func engineWithObserver(t *testing.T) (*Engine, func() string) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	e, err := NewEngine(nil, NewMockRegistry(), failingFetchServices{NewMockHostServices()}, zap.New(core))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = e.runtime.Close(context.Background()) })
	return e, func() string {
		var out []string
		for _, entry := range logs.All() {
			out = append(out, fmt.Sprintf("%s %v", entry.Message, entry.ContextMap()))
		}
		return strings.Join(out, "\n")
	}
}

func TestHTTPFetchHostFunction_failureLogDropsTheQueryString(t *testing.T) {
	e, logged := engineWithObserver(t)
	ctx := context.Background()
	mod, err := e.runtime.Instantiate(ctx, guestMemory)
	if err != nil {
		t.Fatal(err)
	}
	mod.Memory().Write(0, []byte("GET"))
	mod.Memory().Write(16, []byte(engineFetchURL))

	if got := e.hHTTPFetch(ctx, mod, 0, 3, 16, uint32(len(engineFetchURL)), 0, 0, 0, 0); got != 0 {
		t.Fatalf("a failed fetch returned %d", got)
	}
	log := logged()
	if log == "" {
		t.Fatal("nothing was logged, so the test checks nothing")
	}
	for _, leaked := range []string{engineFetchSecret, "api_key"} {
		if strings.Contains(log, leaked) {
			t.Errorf("the http_fetch failure log quotes the query (%q): %s", leaked, log)
		}
	}
	if !strings.Contains(log, engineFetchHost+"/private/path") || !strings.Contains(log, "connection reset") {
		t.Errorf("the log lost the endpoint or the cause: %s", log)
	}
}

func TestAnonFetchHostFunction_failureLogNamesNoDestination(t *testing.T) {
	e, logged := engineWithObserver(t)
	ctx := context.Background()
	mod, err := e.runtime.Instantiate(ctx, guestMemory)
	if err != nil {
		t.Fatal(err)
	}
	mod.Memory().Write(0, []byte("GET"))
	mod.Memory().Write(16, []byte(engineFetchURL))

	if got := e.hAnonFetch(ctx, mod, 0, 3, 16, uint32(len(engineFetchURL)), 0, 0, 0, 0); got != 0 {
		t.Fatalf("a failed fetch returned %d", got)
	}
	log := logged()
	if log == "" {
		t.Fatal("nothing was logged, so the test checks nothing")
	}
	for _, leaked := range []string{engineFetchHost, "/private/path", engineFetchSecret, "api_key", "url:"} {
		if strings.Contains(log, leaked) {
			t.Errorf("the anon_fetch failure log names %q: %s", leaked, log)
		}
	}
	if !strings.Contains(log, "error_class:transport") {
		t.Errorf("the log lost its error class: %s", log)
	}
}
