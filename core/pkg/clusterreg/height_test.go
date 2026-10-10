package clusterreg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchLatestHeight(t *testing.T) {
	for body, want := range map[string]uint64{`{"block":{"header":{"height":"4821"}}}`: 4821} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != latestBlockPath {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		}))
		got, err := FetchLatestHeight(context.Background(), srv.URL)
		srv.Close()
		if err != nil || got != want {
			t.Errorf("%s: %d, %v", body, got, err)
		}
	}
}

func TestFetchLatestHeight_refusesWhatIsNotAHeight(t *testing.T) {
	for _, body := range []string{`{}`, `{"block":{"header":{"height":"0"}}}`, `{"block":{"header":{"height":"-5"}}}`,
		`{"block":{"header":{"height":"99999999999999999999999"}}}`, `not json`, `{"block":{"header":{"height":"1\u001b[2J"}}}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		if h, err := FetchLatestHeight(context.Background(), srv.URL); err == nil {
			t.Errorf("%s accepted as %d", body, h)
		}
		srv.Close()
	}
}
