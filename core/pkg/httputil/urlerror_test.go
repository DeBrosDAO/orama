package httputil

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFailureReason_dropsTheRequestURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()

	_, err := http.Get(addr + "/v1/db?api_key=SECRET")
	if err == nil {
		t.Fatal("expected a dial error")
	}
	if !strings.Contains(err.Error(), "api_key=SECRET") {
		t.Fatalf("precondition: the client error should quote the URL, got %q", err)
	}
	got := FailureReason(err)
	if strings.Contains(got, "SECRET") || strings.Contains(got, "api_key") {
		t.Fatalf("FailureReason kept the URL: %q", got)
	}
	if got == "" {
		t.Fatal("FailureReason is empty")
	}
}

func TestFailureReason_wrappedURLError(t *testing.T) {
	_, err := http.Get("http://127.0.0.1:1/x?token=SECRET")
	got := FailureReason(fmt.Errorf("hop: %w", err))
	if strings.Contains(got, "SECRET") {
		t.Fatalf("FailureReason kept the URL of a wrapped error: %q", got)
	}
}

func TestFailureReason_plainError(t *testing.T) {
	if got := FailureReason(errors.New("boom")); got != "boom" {
		t.Fatalf("got %q", got)
	}
}

func TestWithoutQuery(t *testing.T) {
	cases := map[string]string{
		"https://user:pw@example.com/a/b?api_key=SECRET#frag": "https://example.com/a/b",
		"http://10.0.0.1:6001/v1/x":                           "http://10.0.0.1:6001/v1/x",
		"http://h/p?":                                         "http://h/p",
		"":                                                    "",
	}
	for in, want := range cases {
		if got := WithoutQuery(in); got != want {
			t.Errorf("WithoutQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithoutQuery_unparseable(t *testing.T) {
	got := WithoutQuery("http://a b/?api_key=SECRET\x7f")
	if strings.Contains(got, "SECRET") {
		t.Fatalf("an unparseable URL was repeated: %q", got)
	}
}

func TestWithoutURL_keepsTheCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:1/x?token=SECRET", nil)
	_, err := http.DefaultClient.Do(req)
	got := WithoutURL(err)
	if strings.Contains(got.Error(), "SECRET") {
		t.Fatalf("WithoutURL kept the URL: %q", got)
	}
	if !errors.Is(got, context.Canceled) {
		t.Fatalf("WithoutURL lost the cause: %v", got)
	}
}

func TestWithoutURL_plainErrorIsUnchanged(t *testing.T) {
	err := errors.New("boom")
	if WithoutURL(err) != err {
		t.Fatal("a plain error was replaced")
	}
}
