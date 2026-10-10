package gw

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

func streamClient(t *testing.T, h http.Handler) (*Client, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	rec, err := evidence.New(dir, "gw", nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewWithTLS(srv.URL, nil, rec)
	if err != nil {
		t.Fatal(err)
	}
	return c.For(t), filepath.Join(dir, "gw.jsonl")
}

func TestStream_parsesEventsAndRecords(t *testing.T) {
	c, evFile := streamClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != eventStreamType {
			http.Error(w, "want an event stream", http.StatusNotAcceptable)
			return
		}
		w.Header().Set("Content-Type", eventStreamType)
		fmt.Fprint(w, ": keep-alive\n\nretry: 1500\n\nid: 1\nevent: msg\ndata: hello\ndata: world\n\ndata:no-space\r\n\r\n")
		w.(http.Flusher).Flush()
	}))
	s, err := c.Stream(context.Background(), Req{Path: "/v1/pubsub/sse"})
	if err != nil {
		t.Fatal(err)
	}
	// retry and a comment dispatch nothing; retry carries to the next event.
	want := []Event{{ID: "1", Event: "msg", Data: "hello\nworld", Retry: 1500}, {Data: "no-space"}}
	var got []Event
	for {
		ev, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events %+v", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.Close() != nil {
		t.Fatal("a second Close failed")
	}
	raw, _ := os.ReadFile(evFile)
	if strings.Count(string(raw), "STREAM GET") != 1 || !strings.Contains(string(raw), "data=hello") {
		t.Fatalf("evidence %s", raw)
	}
}

func TestStream_errorStatusAndCancel(t *testing.T) {
	c, _ := streamClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/denied" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", eventStreamType)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	s, err := c.Stream(context.Background(), Req{Path: "/denied"})
	if err != nil || s.Status != http.StatusUnauthorized {
		t.Fatalf("status %v err %v", s, err)
	}
	_ = s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	s, err = c.Stream(ctx, Req{Path: "/open"})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := s.Next(); err == nil {
		t.Fatal("Next returned an event after the context ended")
	}
	_ = s.Close()
}

// TestMustSend_fromCleanupStillSends is the regression test for MustSend
// using t.Context(), which is cancelled before cleanups run.
func TestMustSend_fromCleanupStillSends(t *testing.T) {
	var hits atomic.Int32
	c, _ := streamClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Run("sub", func(t *testing.T) {
		t.Cleanup(func() {
			if resp := c.MustSend(t, Req{Method: http.MethodDelete, Path: "/v1/thing"}); resp.Status != http.StatusNoContent {
				t.Errorf("status %d", resp.Status)
			}
		})
	})
	if hits.Load() != 1 {
		t.Fatalf("the cleanup's request reached the server %d times", hits.Load())
	}
}
