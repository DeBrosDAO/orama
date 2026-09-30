package cache

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/olric/olrictest"
	"go.uber.org/zap"
)

// deadMemberHandlers is a handler whose cache member has gone away: the Olric
// error for that names the member's address.
func deadMemberHandlers(t *testing.T) (*CacheHandlers, string) {
	t.Helper()
	srv := olrictest.Start(t)
	client, err := olric.NewClient(olric.Config{Servers: []string{srv.Addr}}, zap.NewNop())
	if err != nil {
		t.Fatalf("olric.NewClient: %v", err)
	}
	logger, err := logging.NewColoredLogger(logging.ComponentGeneral, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	srv.Stop(t)
	return &CacheHandlers{logger: logger, olricClient: client}, srv.Addr
}

func post(h func(http.ResponseWriter, *http.Request), path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat"))
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

// What a client is told when the cache fails is a constant: Olric's error
// names member addresses on the overlay, and belongs in the log.
func TestCacheHandlers_aFailingCacheShowsTheClientNoOlricError(t *testing.T) {
	h, addr := deadMemberHandlers(t)
	host, port, _ := net.SplitHostPort(addr)

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"delete": post(h.DeleteHandler, "/v1/cache/delete", `{"dmap":"d","key":"k"}`),
		"get":    post(h.GetHandler, "/v1/cache/get", `{"dmap":"d","key":"k"}`),
		"list":   post(h.ScanHandler, "/v1/cache/scan", `{"dmap":"d"}`),
	} {
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status %d, want 500: %s", name, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, leak := range []string{host, port, "refused", "dial", "%!"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s: the response carries the Olric error (%q): %s", name, leak, body)
			}
		}
	}
}
