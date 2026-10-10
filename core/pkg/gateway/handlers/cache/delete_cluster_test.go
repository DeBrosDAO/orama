package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/olric/olrictest"
	"go.uber.org/zap"
)

// deleteKeysCount spreads over the partitions of both members: a key owned by
// the member the client did not pick is what Olric's Delete reported as 0.
const deleteKeysCount = 40

func del(t *testing.T, h *CacheHandlers, dmap, key string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(DeleteRequest{DMap: dmap, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/cache/delete", strings.NewReader(string(encoded)))
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat"))
	rec := httptest.NewRecorder()
	h.DeleteHandler(rec, r)
	return rec
}

// Every stored key deletes with 200, wherever its partition lives, and is gone
// afterwards. Before, a key owned by another member answered 404 "key not
// found" — after being deleted (stagenet contracts-live, 2026-09-30).
func TestDeleteHandler_aKeyOnAnyMemberDeletes(t *testing.T) {
	members := olrictest.StartCluster(t, 2)
	client, err := olric.NewClient(olric.Config{Servers: []string{members[0].Addr, members[1].Addr}}, zap.NewNop())
	if err != nil {
		t.Fatalf("olric.NewClient: %v", err)
	}
	h := &CacheHandlers{olricClient: client}

	for i := 0; i < deleteKeysCount; i++ {
		key := fmt.Sprintf("user-%d", i)
		if rec := put(t, h, PutRequest{DMap: "sessions", Key: key, Value: "v"}); rec.Code != http.StatusOK {
			t.Fatalf("put %s: %d %s", key, rec.Code, rec.Body.String())
		}
		if rec := del(t, h, "sessions", key); rec.Code != http.StatusOK {
			t.Fatalf("delete of stored %s: %d %s", key, rec.Code, rec.Body.String())
		}
		if rec := del(t, h, "sessions", key); rec.Code != http.StatusNotFound {
			t.Fatalf("second delete of %s: %d, want 404 (it is gone)", key, rec.Code)
		}
	}
}

// Deleting what was never stored is a 404, not a silent 200.
func TestDeleteHandler_aMissingKeyIs404(t *testing.T) {
	h, _ := handlersWithOlric(t)
	if rec := del(t, h, "sessions", "never-set"); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
