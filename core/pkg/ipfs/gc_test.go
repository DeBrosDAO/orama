package ipfs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPIEndpoint(t *testing.T) {
	url, token, err := APIEndpoint("/ip4/127.0.0.1/tcp/10102", "bearer:abc123")
	if err != nil || url != "http://127.0.0.1:10102" || token != "abc123" {
		t.Fatalf("APIEndpoint = %q, %q, %v", url, token, err)
	}
	for name, c := range map[string]struct{ addr, auth string }{
		"empty address":     {"", "bearer:abc"},
		"not a multiaddr":   {"127.0.0.1:10102", "bearer:abc"},
		"not a tcp address": {"/ip4/127.0.0.1/udp/10102", "bearer:abc"},
		"no credential":     {"/ip4/127.0.0.1/tcp/10102", ""},
		"basic credential":  {"/ip4/127.0.0.1/tcp/10102", "basic:user:pass"},
		"empty bearer":      {"/ip4/127.0.0.1/tcp/10102", "bearer:"},
	} {
		if _, _, err := APIEndpoint(c.addr, c.auth); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func gcServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRepoGC_countsRemovedBlocksAndSendsTheBearer(t *testing.T) {
	var method, auth, path string
	base := gcServer(t, func(w http.ResponseWriter, r *http.Request) {
		method, auth, path = r.Method, r.Header.Get("Authorization"), r.URL.Path
		_, _ = w.Write([]byte(`{"Key":{"/":"bafy1"}}` + "\n" + `{"Key":{"/":"bafy2"}}` + "\n"))
	})
	n, err := RepoGC(context.Background(), base, "tok")
	if err != nil || n != 2 {
		t.Fatalf("RepoGC = %d, %v; want 2 blocks", n, err)
	}
	if method != http.MethodPost || path != "/api/v0/repo/gc" || auth != "Bearer tok" {
		t.Errorf("request = %s %s with %q", method, path, auth)
	}
}

func TestRepoGC_emptyRepoRemovesNothing(t *testing.T) {
	base := gcServer(t, func(w http.ResponseWriter, _ *http.Request) {})
	if n, err := RepoGC(context.Background(), base, "tok"); err != nil || n != 0 {
		t.Fatalf("RepoGC = %d, %v; want 0 and no error", n, err)
	}
}

func TestRepoGC_refusedBearerIsAnError(t *testing.T) {
	base := gcServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid authorization", http.StatusUnauthorized)
	})
	_, err := RepoGC(context.Background(), base, "wrong")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid authorization") {
		t.Fatalf("err = %v, want the 401 and Kubo's reason", err)
	}
}

func TestRepoGC_failedBlocksAreAnErrorAfterTheStream(t *testing.T) {
	base := gcServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Key":{"/":"bafy1"}}` + "\n" + `{"Error":"block bafy2 is locked"}` + "\n" + `{"Key":{"/":"bafy3"}}` + "\n"))
	})
	n, err := RepoGC(context.Background(), base, "tok")
	if err == nil || n != 2 || !strings.Contains(err.Error(), "bafy2 is locked") {
		t.Fatalf("RepoGC = %d, %v; want 2 removed and the failed block named", n, err)
	}
}

func TestRepoGC_truncatedStreamIsAnError(t *testing.T) {
	base := gcServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Key":{"/":"bafy1"}}` + "\n" + `{"Key":`))
	})
	if _, err := RepoGC(context.Background(), base, "tok"); err == nil {
		t.Fatal("a stream cut in the middle of an entry was taken for a finished collection")
	}
}

func TestRepoGC_unreachableDaemonIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	if _, err := RepoGC(context.Background(), base, "tok"); err == nil || !strings.Contains(err.Error(), base) {
		t.Fatalf("err = %v, want it to name %s", err, base)
	}
}

// Stopping the caller ends a collection that is still running, at once: the
// request is cancelled and the daemon sees the client go.
func TestRepoGC_cancelEndsARunningCollection(t *testing.T) {
	started := make(chan struct{})
	base := gcServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Key":{"/":"bafy1"}}` + "\n"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := RepoGC(ctx, base, "tok")
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("RepoGC kept waiting after its context was cancelled")
	}
}
