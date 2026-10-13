//go:build e2e_fleet

package cache

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

// Cache routes (docs/whitepaper/technical-reference/appendices/i-api-surface.md "Cache"); request and response shapes are
// core/pkg/gateway/handlers/cache.
const (
	pathGet    = "/v1/cache/get"
	pathPut    = "/v1/cache/put"
	pathDelete = "/v1/cache/delete"
	pathMGet   = "/v1/cache/mget"
	pathScan   = "/v1/cache/scan"
	pathHealth = "/v1/cache/health"
	// ttlShort is a TTL long enough to read back once, short enough to wait out.
	ttlShort = 3 * time.Second
	// expiryBudget covers Olric's expiry sweep on top of the TTL.
	expiryBudget = 30 * time.Second
	pollEvery    = time.Second
)

type entry struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
	DMap  string `json:"dmap"`
}

func put(t testing.TB, c *gw.Client, who tenancy.Cred, dmap, key string, value any, ttl string) *gw.Response {
	t.Helper()
	body := map[string]any{"dmap": dmap, "key": key, "value": value}
	if ttl != "" {
		body["ttl"] = ttl
	}
	return tenancy.Post(t, c, pathPut, who, body)
}

func get(t testing.TB, c *gw.Client, who tenancy.Cred, dmap, key string) *gw.Response {
	t.Helper()
	return tenancy.Post(t, c, pathGet, who, map[string]any{"dmap": dmap, "key": key})
}

// mustGet reads key and returns its value, failing unless it is found.
func mustGet(t testing.TB, c *gw.Client, who tenancy.Cred, dmap, key string) any {
	t.Helper()
	var e entry
	if err := get(t, c, who, dmap, key).Expect(t, http.StatusOK).Decode(&e); err != nil {
		t.Fatal(err)
	}
	if e.Key != key || e.DMap != dmap {
		t.Fatalf("get %s/%s answered for %s/%s", dmap, key, e.DMap, e.Key)
	}
	return e.Value
}

// TestCache_roundTripsEveryJSONType puts every JSON type and reads it back
// unchanged (core/pkg/gateway/handlers/cache/set_handler.go: objects and arrays
// are stored as JSON, scalars as themselves).
func TestCache_roundTripsEveryJSONType(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	values := map[string]any{
		"string": "hello", "number": 42.5, "bool": true,
		"object": map[string]any{"name": "e2e", "nested": map[string]any{"n": 1.0}},
		"array":  []any{"a", 2.0, false},
		"rtl":    "abc\u202edef\u0000ghi",
		// A string that parses as JSON must still come back a string.
		"numeric-string": "123", "json-string": `{"a":1}`,
		"unicode": "héllo \u202e 𝓊𝓃𝒾𝒸ℴ𝒹ℯ 漢字 é",
	}
	for key, v := range values {
		put(t, n.Client, tenancy.Owner(n), "types", key, v, "").Expect(t, http.StatusOK)
	}
	for key, want := range values {
		if got := mustGet(t, n.Client, tenancy.Owner(n), "types", key); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: read back %#v, want %#v", key, got, want)
		}
	}
}

func TestCache_overwriteAndDelete(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	put(t, n.Client, tenancy.Owner(n), "m", "k", "v1", "").Expect(t, http.StatusOK)
	put(t, n.Client, tenancy.Owner(n), "m", "k", "v2", "").Expect(t, http.StatusOK)
	if got := mustGet(t, n.Client, tenancy.Owner(n), "m", "k"); got != "v2" {
		t.Fatalf("after overwrite read %v, want v2", got)
	}
	tenancy.Post(t, n.Client, pathDelete, tenancy.Owner(n), map[string]any{"dmap": "m", "key": "k"}).Expect(t, http.StatusOK)
	get(t, n.Client, tenancy.Owner(n), "m", "k").Expect(t, http.StatusNotFound)
	// Deleting what is not there is a 404, not a silent 200 (delete_handler.go).
	tenancy.Post(t, n.Client, pathDelete, tenancy.Owner(n), map[string]any{"dmap": "m", "key": "k"}).Expect(t, http.StatusNotFound)
}

func TestCache_missingKeyAndMapAre404(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	get(t, n.Client, tenancy.Owner(n), "never-written", "nope").Expect(t, http.StatusNotFound)
	put(t, n.Client, tenancy.Owner(n), "m", "present", 1, "").Expect(t, http.StatusOK)
	get(t, n.Client, tenancy.Owner(n), "m", "absent").Expect(t, http.StatusNotFound)
}

// TestCache_ttlExpires: a TTL entry is readable, then gone; an entry without a
// TTL outlives it (set_handler.go putOptionsForTTL).
func TestCache_ttlExpires(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	put(t, n.Client, tenancy.Owner(n), "ttl", "short", "x", ttlShort.String()).Expect(t, http.StatusOK)
	put(t, n.Client, tenancy.Owner(n), "ttl", "forever", "y", "0s").Expect(t, http.StatusOK)
	mustGet(t, n.Client, tenancy.Owner(n), "ttl", "short")
	eventually.Require(t, pollEvery, ttlShort+expiryBudget, "the ttl entry to expire", func() (bool, error) {
		resp := get(t, n.Client, tenancy.Owner(n), "ttl", "short")
		if resp.Status == http.StatusNotFound {
			return true, nil
		}
		return false, fmt.Errorf("still HTTP %d", resp.Status)
	})
	if got := mustGet(t, n.Client, tenancy.Owner(n), "ttl", "forever"); got != "y" {
		t.Fatalf("the no-expiry entry read %v after the ttl one expired", got)
	}
}

func TestCache_invalidTTLRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	// olric.MaxEntryTTL is ten years (core/pkg/olric/ttl.go): 87600h.
	for _, ttl := range []string{"-1s", "soon", "1 hour", "87601h", "9999999999999h"} {
		if resp := put(t, n.Client, tenancy.Owner(n), "m", "k", "v", ttl); resp.Status != http.StatusBadRequest {
			t.Errorf("ttl %q: want 400, got %d: %s", ttl, resp.Status, resp.Body)
		}
	}
	get(t, n.Client, tenancy.Owner(n), "m", "k").Expect(t, http.StatusNotFound)
	put(t, n.Client, tenancy.Owner(n), "m", "k", "v", "87600h").Expect(t, http.StatusOK)
}

// TestCache_mgetReturnsOnlyFoundKeys: missing keys are skipped, not errors
// (get_handler.go MultiGetHandler).
func TestCache_mgetReturnsOnlyFoundKeys(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for _, k := range []string{"a", "b", "c"} {
		put(t, n.Client, tenancy.Owner(n), "m", k, "v-"+k, "").Expect(t, http.StatusOK)
	}
	var out struct {
		Results []entry `json:"results"`
	}
	resp := tenancy.Post(t, n.Client, pathMGet, tenancy.Owner(n), map[string]any{"dmap": "m", "keys": []string{"a", "missing", "c", ""}})
	if err := resp.Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, e := range out.Results {
		got[e.Key] = e.Value
	}
	if want := map[string]any{"a": "v-a", "c": "v-c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("mget returned %v, want %v", got, want)
	}
	tenancy.Post(t, n.Client, pathMGet, tenancy.Owner(n), map[string]any{"dmap": "m", "keys": []string{}}).Expect(t, http.StatusBadRequest)
}

// TestCache_scanListsAndFilters: scan returns every key of the map and applies
// the regex in match. The route has no pagination (list_handler.go): the whole
// key set comes back in one answer, which this test pins at a few hundred keys.
func TestCache_scanListsAndFilters(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	const total = 250
	var want []string
	for i := range total {
		k := fmt.Sprintf("user:%04d", i)
		want = append(want, k)
		put(t, n.Client, tenancy.Owner(n), "scan", k, i, "").Expect(t, http.StatusOK)
	}
	put(t, n.Client, tenancy.Owner(n), "scan", "other:1", 1, "").Expect(t, http.StatusOK)
	keys := scanKeys(t, n, "scan", "^user:")
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("scan ^user: returned %d keys, want %d", len(keys), len(want))
	}
	if all := scanKeys(t, n, "scan", ""); len(all) != total+1 {
		t.Fatalf("scan without match returned %d keys, want %d", len(all), total+1)
	}
	if none := scanKeys(t, n, "empty-map", ""); len(none) != 0 {
		t.Fatalf("scan of an empty map returned %v", none)
	}
}

func scanKeys(t testing.TB, n *ns.Namespace, dmap, match string) []string {
	t.Helper()
	var out struct {
		Keys  []string `json:"keys"`
		Count int      `json:"count"`
	}
	resp := tenancy.Post(t, n.Client, pathScan, tenancy.Owner(n), map[string]any{"dmap": dmap, "match": match})
	if err := resp.Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Count != len(out.Keys) {
		t.Errorf("scan count %d but %d keys", out.Count, len(out.Keys))
	}
	sort.Strings(out.Keys)
	return out.Keys
}

func TestCache_healthReportsOlric(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	var out map[string]any
	if err := tenancy.Send(t, n.Client, http.MethodGet, pathHealth, tenancy.Owner(n), nil).Expect(t, http.StatusOK).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "ok" || out["service"] != "olric" {
		t.Fatalf("cache health answered %v", out)
	}
}
