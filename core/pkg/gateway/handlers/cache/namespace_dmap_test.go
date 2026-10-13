package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/olric"
	"github.com/DeBrosOfficial/network/pkg/olric/olrictest"
	"go.uber.org/zap"
)

const (
	// boundTestMaxInuse is the per-DMap memory bound the bound test runs Olric
	// with: small enough to reach, large enough that Olric's per-partition share
	// of it holds several of the test's values.
	boundTestMaxInuse = 8 << 20
	boundTestDMaps    = 64
	boundTestKeys     = 40
	boundTestValueLen = 8 << 10
)

// Bug: every tenant dmap was its own Olric DMap, each with its own
// maxInuse bound, so a namespace's cache was bounded by the number of dmaps the
// tenant cared to make, not by the bound: enough of them reached the unit's
// MemoryMax and the OOM kill lost every key. The namespace's whole cache is one
// DMap now: 64 dmaps holding 20 MiB in total against an 8 MiB bound are held
// to that bound.
func TestCache_manyDMapsShareOneMemoryBound(t *testing.T) {
	srv := olrictest.StartBounded(t, boundTestMaxInuse)
	client, err := olric.NewClient(olric.Config{Servers: []string{srv.Addr}}, zap.NewNop())
	if err != nil {
		t.Fatalf("olric.NewClient: %v", err)
	}
	h := &CacheHandlers{olricClient: client}

	value := strings.Repeat("v", boundTestValueLen)
	for d := 0; d < boundTestDMaps; d++ {
		for k := 0; k < boundTestKeys; k++ {
			rec := put(t, h, PutRequest{DMap: fmt.Sprintf("dmap-%d", d), Key: fmt.Sprintf("key-%d", k), Value: value})
			if rec.Code != http.StatusOK {
				t.Fatalf("put dmap-%d/key-%d: %d %s", d, k, rec.Code, rec.Body.String())
			}
		}
	}

	held := 0
	for d := 0; d < boundTestDMaps; d++ {
		held += len(scanKeysOf(t, h, fmt.Sprintf("dmap-%d", d), ""))
	}
	written := boundTestDMaps * boundTestKeys
	if held == 0 || held >= written {
		t.Fatalf("%d of %d entries held: eviction must keep some and drop some", held, written)
	}
	// Entries cost their value plus key and overhead; allow a quarter over the bound.
	if heldBytes, ceiling := held*boundTestValueLen, boundTestMaxInuse+boundTestMaxInuse/4; heldBytes > ceiling {
		t.Fatalf("the namespace holds %d bytes across %d dmaps, over the %d its one bound allows", heldBytes, boundTestDMaps, ceiling)
	}
}

func scanKeysOf(t *testing.T, h *CacheHandlers, dmap, match string) []string {
	t.Helper()
	rec := postJSON(t, h.ScanHandler, "/v1/cache/scan", ScanRequest{DMap: dmap, Match: match})
	if rec.Code != http.StatusOK {
		t.Fatalf("scan %s: %d %s", dmap, rec.Code, rec.Body.String())
	}
	var out struct {
		Keys []string `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out.Keys)
	return out.Keys
}

// Dmaps stay apart in the one DMap: the same key in two dmaps is two entries,
// and a scan lists the keys of its dmap only, as the tenant named them, even
// when another dmap's name begins with this one's or looks like a key of it.
func TestCache_dmapsStayApartInTheNamespaceDMap(t *testing.T) {
	h, _ := handlersWithOlric(t)
	entries := []struct{ dmap, key, value string }{
		{"a", "k", "in a"},
		{"ab", "k", "in ab"},
		{"a", "bk", "in a, key bk"},
		{"1:a", "k", "in 1:a"},
		{"a.*", "k", "regex-looking dmap"},
	}
	for _, e := range entries {
		if rec := put(t, h, PutRequest{DMap: e.dmap, Key: e.key, Value: e.value}); rec.Code != http.StatusOK {
			t.Fatalf("put %s/%s: %d", e.dmap, e.key, rec.Code)
		}
	}
	for _, e := range entries {
		if got := getValue(t, h, e.dmap, e.key); got != e.value {
			t.Errorf("%s/%s read %v, want %q", e.dmap, e.key, got, e.value)
		}
	}
	want := map[string][]string{"a": {"bk", "k"}, "ab": {"k"}, "1:a": {"k"}, "a.*": {"k"}, "unused": nil}
	for dmap, keys := range want {
		if got := scanKeysOf(t, h, dmap, ""); !reflect.DeepEqual(got, keys) && (len(got) != 0 || len(keys) != 0) {
			t.Errorf("scan %q = %v, want %v", dmap, got, keys)
		}
	}
}

// The caller's pattern is written against its keys, not against the key the
// store holds: ^user: must still anchor at the start of the tenant's key.
func TestScanHandler_matchAppliesToTheTenantKey(t *testing.T) {
	h, _ := handlersWithOlric(t)
	for _, key := range []string{"user:1", "user:2", "other:user:3"} {
		put(t, h, PutRequest{DMap: "m", Key: key, Value: 1})
	}
	if got := scanKeysOf(t, h, "m", "^user:"); !reflect.DeepEqual(got, []string{"user:1", "user:2"}) {
		t.Errorf("^user: matched %v", got)
	}
	if got := scanKeysOf(t, h, "m", "user:3$"); !reflect.DeepEqual(got, []string{"other:user:3"}) {
		t.Errorf("user:3$ matched %v", got)
	}
	if got := scanKeysOf(t, h, "m", "^1:m"); len(got) != 0 {
		t.Errorf("a pattern reached the stored prefix: matched %v", got)
	}
	if rec := postJSON(t, h.ScanHandler, "/v1/cache/scan", ScanRequest{DMap: "m", Match: "("}); rec.Code != http.StatusBadRequest {
		t.Errorf("an invalid pattern answered %d, want 400", rec.Code)
	}
}

// A key too long to be stored under its dmap is refused on put with the limit
// that applies, and is simply not there for get, delete and mget.
func TestCache_aKeyOverTheDMapsLimit(t *testing.T) {
	h, _ := handlersWithOlric(t)
	long := strings.Repeat("k", maxKeyBytesIn("lim")+1)

	rec := postJSON(t, h.SetHandler, "/v1/cache/put", PutRequest{DMap: "lim", Key: long, Value: 1})
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), fmt.Sprint(maxKeyBytesIn("lim"))) {
		t.Fatalf("put: %d %s, want 413 naming the %d-byte limit", rec.Code, rec.Body.String(), maxKeyBytesIn("lim"))
	}
	if rec := postJSON(t, h.GetHandler, "/v1/cache/get", GetRequest{DMap: "lim", Key: long}); rec.Code != http.StatusNotFound {
		t.Errorf("get: %d, want 404", rec.Code)
	}
	if rec := postJSON(t, h.DeleteHandler, "/v1/cache/delete", DeleteRequest{DMap: "lim", Key: long}); rec.Code != http.StatusNotFound {
		t.Errorf("delete: %d, want 404", rec.Code)
	}
	if rec := postJSON(t, h.MultiGetHandler, "/v1/cache/mget", MultiGetRequest{DMap: "lim", Keys: []string{long}}); rec.Code != http.StatusOK {
		t.Errorf("mget: %d, want 200 with nothing found", rec.Code)
	}
}

func TestFoldKey_isUnambiguous(t *testing.T) {
	seen := map[string]string{}
	for _, dmap := range []string{"a", "ab", "1:a", "11:a", "", ":", "a:b", "10:aaaaaaaaaa"} {
		for _, key := range []string{"", "b", "k", "1:a", "a:b", ":", "0:"} {
			folded, ok := foldKey(dmap, key)
			if !ok {
				t.Fatalf("%q/%q does not fit", dmap, key)
			}
			id := fmt.Sprintf("%q/%q", dmap, key)
			if prev, dup := seen[folded]; dup {
				t.Errorf("%s and %s both fold to %q", prev, id, folded)
			}
			seen[folded] = id
			if got, ok := unfoldKey(dmap, folded); !ok || got != key {
				t.Errorf("%s unfolds to %q, %v", id, got, ok)
			}
		}
	}
	folded, _ := foldKey("ab", "k")
	if _, ok := unfoldKey("a", folded); ok {
		t.Error("a key of dmap ab was read as a key of dmap a")
	}
}

func TestFoldKey_limit(t *testing.T) {
	if _, ok := foldKey("lim", strings.Repeat("k", maxKeyBytesIn("lim"))); !ok {
		t.Error("the longest key for the dmap was refused")
	}
	if _, ok := foldKey("lim", strings.Repeat("k", maxKeyBytesIn("lim")+1)); ok {
		t.Error("a key one byte over was accepted")
	}
	if _, ok := foldKey(strings.Repeat("d", MaxKeyBytes), ""); ok {
		t.Error("a dmap name filling the limit was accepted")
	}
}

// A scan's match pattern is compiled by the gateway, so its length is bounded.
func TestScanHandler_refusesAnOverlongMatchPattern(t *testing.T) {
	h := &CacheHandlers{}
	body := `{"dmap":"d","match":"` + strings.Repeat("a", MaxMatchBytes+1) + `"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/cache/scan", strings.NewReader(body))
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "ns"))
	w := httptest.NewRecorder()
	h.ScanHandler(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (%s), want 400 for a pattern over %d bytes", w.Code, w.Body.String(), MaxMatchBytes)
	}
}
