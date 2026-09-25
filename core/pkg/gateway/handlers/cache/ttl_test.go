package cache

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/olric/olrictest"
	"go.uber.org/zap"
)

// bug-421: /v1/cache/put parsed ttl and then dropped it, so every entry was
// immortal. These run the handler against a real Olric, through the same
// cluster-client path a gateway uses.

func handlersWithOlric(t *testing.T) (*CacheHandlers, *olric.Client) {
	t.Helper()
	srv := olrictest.Start(t)
	client, err := olric.NewClient(olric.Config{Servers: []string{srv.Addr}}, zap.NewNop())
	if err != nil {
		t.Fatalf("olric.NewClient: %v", err)
	}
	return &CacheHandlers{olricClient: client}, client
}

func put(t *testing.T, h *CacheHandlers, body PutRequest) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/cache/put", strings.NewReader(string(encoded)))
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat"))
	rec := httptest.NewRecorder()
	h.SetHandler(rec, r)
	return rec
}

func storedTTLMillis(t *testing.T, c *olric.Client, dmap, key string) int64 {
	t.Helper()
	dm, err := c.GetClient().NewDMap("anchat:" + dmap)
	if err != nil {
		t.Fatalf("NewDMap: %v", err)
	}
	res, err := dm.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("reading %q back: %v", key, err)
	}
	return res.TTL()
}

func TestSetHandler_ttlIsStored(t *testing.T) {
	h, c := handlersWithOlric(t)

	rec := put(t, h, PutRequest{DMap: "prices", Key: "sol", Value: "1.23", TTL: "45s"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ttl := storedTTLMillis(t, c, "prices", "sol"); ttl <= 0 {
		t.Fatalf("stored TTL = %d, want an expiry for ttl=45s", ttl)
	}
}

func TestSetHandler_noTTLHasNoExpiry(t *testing.T) {
	h, c := handlersWithOlric(t)

	for _, ttl := range []string{"", "0s"} {
		key := "k" + ttl
		rec := put(t, h, PutRequest{DMap: "prices", Key: key, Value: "v", TTL: ttl})
		if rec.Code != http.StatusOK {
			t.Fatalf("ttl=%q: status = %d: %s", ttl, rec.Code, rec.Body.String())
		}
		if got := storedTTLMillis(t, c, "prices", key); got != 0 {
			t.Errorf("ttl=%q: stored TTL = %d, want 0 (no expiry)", ttl, got)
		}
	}
}

func TestSetHandler_badTTLRefusedAndNotStored(t *testing.T) {
	h, c := handlersWithOlric(t)

	for _, ttl := range []string{"soon", "-1m", "100000h"} {
		rec := put(t, h, PutRequest{DMap: "prices", Key: "bad", Value: "v", TTL: ttl})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("ttl=%q: status = %d, want 400", ttl, rec.Code)
		}
	}
	dm, err := c.GetClient().NewDMap("anchat:prices")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dm.Get(context.Background(), "bad"); err == nil {
		t.Fatal("a refused write left an entry behind")
	}
}

func TestPutOptionsForTTL_bounds(t *testing.T) {
	if _, err := putOptionsForTTL(olric.MaxEntryTTL.String()); err != nil {
		t.Errorf("ttl at the maximum refused: %v", err)
	}
	if _, err := putOptionsForTTL((olric.MaxEntryTTL + 1).String()); err == nil {
		t.Error("ttl just over the maximum accepted; it would overflow Olric's expiry")
	}
}
