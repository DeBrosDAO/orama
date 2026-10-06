package cache

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

func postJSON(t *testing.T, call func(http.ResponseWriter, *http.Request), path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))
	r = r.WithContext(context.WithValue(r.Context(), ctxkeys.NamespaceOverride, "anchat"))
	rec := httptest.NewRecorder()
	call(rec, r)
	return rec
}

func getValue(t *testing.T, h *CacheHandlers, dmap, key string) any {
	t.Helper()
	rec := postJSON(t, h.GetHandler, "/v1/cache/get", GetRequest{DMap: dmap, Key: key})
	if rec.Code != http.StatusOK {
		t.Fatalf("get %s: status %d: %s", key, rec.Code, rec.Body.String())
	}
	var out struct {
		Value any `json:"value"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Value
}

// Bug: a value came back as what it looked like, not as what was put. Olric
// stores bytes with no type, so the string "123" came back as the number 123
// and true as 1.
func TestCache_roundTripsEveryJSONType(t *testing.T) {
	h, _ := handlersWithOlric(t)
	values := map[string]any{
		"string": "hello", "number": 42.5, "integer": 7.0, "zero": 0.0, "negative": -3.0,
		"true": true, "false": false,
		"object":       map[string]any{"name": "e2e", "nested": map[string]any{"n": 1.0}},
		"array":        []any{"a", 2.0, false},
		"empty-object": map[string]any{}, "empty-array": []any{},
		"rtl":            "abc\u202edef\u0000ghi",
		"numeric-string": "123", "bool-string": "true", "null-string": "null", "json-string": `{"a":1}`,
		"empty-string": "", "spaces": "  padded  ",
		"unicode": "héllo \u202e 𝓊𝓃𝒾𝒸ℴ𝒹ℯ 漢字 é",
	}
	for key, v := range values {
		if rec := postJSON(t, h.SetHandler, "/v1/cache/put", PutRequest{DMap: "types", Key: key, Value: v}); rec.Code != http.StatusOK {
			t.Fatalf("put %s: status %d: %s", key, rec.Code, rec.Body.String())
		}
	}
	for key, want := range values {
		if got := getValue(t, h, "types", key); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: read back %#v, want %#v", key, got, want)
		}
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	rec := postJSON(t, h.MultiGetHandler, "/v1/cache/mget", MultiGetRequest{DMap: "types", Keys: keys})
	var many struct {
		Results []struct {
			Key   string `json:"key"`
			Value any    `json:"value"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &many); err != nil {
		t.Fatal(err)
	}
	if len(many.Results) != len(values) {
		t.Fatalf("mget returned %d of %d keys", len(many.Results), len(values))
	}
	for _, r := range many.Results {
		if want := values[r.Key]; !reflect.DeepEqual(r.Value, want) {
			t.Errorf("mget %s: %#v, want %#v", r.Key, r.Value, want)
		}
	}
}

// Values written before entries carried their type are raw text in the store.
// They are still read, as what they look like: the type was never recorded.
func TestCache_readsValuesWrittenBeforeTyping(t *testing.T) {
	h, c := handlersWithOlric(t)
	dm, err := c.GetClient().NewDMap(namespaceDMapName("anchat"))
	if err != nil {
		t.Fatal(err)
	}
	for key, raw := range map[string]any{
		"text": "hello", "number": 42.5, "object": []byte(`{"a":1}`), "array": []byte(`[1,"b"]`),
	} {
		if err := dm.Put(context.Background(), dmapKeyPrefix("legacy")+key, raw); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]any{"text": "hello", "number": 42.5, "object": map[string]any{"a": 1.0}, "array": []any{1.0, "b"}}
	for key, w := range want {
		if got := getValue(t, h, "legacy", key); !reflect.DeepEqual(got, w) {
			t.Errorf("%s: %#v, want %#v", key, got, w)
		}
	}
}

// Bug: a value too big for one Olric table was a 500 with the driver's words.
// It is the caller's request that cannot be honoured: a client error, refused
// with the reason, and nothing stored.
func TestSetHandler_aValueTooBigForTheStoreIsRefusedAsAClientError(t *testing.T) {
	h, _ := handlersWithOlric(t)
	rec := postJSON(t, h.SetHandler, "/v1/cache/put", PutRequest{DMap: "big", Key: "k", Value: strings.Repeat("x", 2<<20)})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413: %.200s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "too large") {
		t.Errorf("the refusal does not say why: %s", rec.Body.String())
	}
	if got := postJSON(t, h.GetHandler, "/v1/cache/get", GetRequest{DMap: "big", Key: "k"}); got.Code != http.StatusNotFound {
		t.Errorf("a refused value was stored: get answered %d", got.Code)
	}

	fits := strings.Repeat("0123456789abcdef", (768<<10)/16)
	if rec := postJSON(t, h.SetHandler, "/v1/cache/put", PutRequest{DMap: "big", Key: "fits", Value: fits}); rec.Code != http.StatusOK {
		t.Fatalf("a 768 KiB value: status %d: %.200s", rec.Code, rec.Body.String())
	}
	if got := getValue(t, h, "big", "fits"); got != fits {
		t.Errorf("the 768 KiB value came back as %d bytes", len(got.(string)))
	}
}

// Olric's error text can name cluster members; the tenant gets a fixed
// message and the detail goes to the log (security review, 2026-09-30).
func TestPutFailure_keepsInternalTextOut(t *testing.T) {
	for err, want := range map[string]int{
		"dial tcp 10.0.0.3:3320: connection refused": http.StatusServiceUnavailable,
		"member 10.0.0.3:3320 failed internally":     http.StatusInternalServerError,
	} {
		status, msg := putFailure(errors.New(err))
		if status != want || strings.Contains(msg, "10.0.0.3") || strings.Contains(msg, "dial") {
			t.Fatalf("%q: status %d (want %d) message %q exposes the internal error", err, status, want, msg)
		}
	}
}

// A gateway from before values were typed reads a value by parsing it as JSON,
// falling back to a string. It must read what this one writes exactly, or the
// gateways of a rolling upgrade (or a rollback) disagree (review, 2026-09-30:
// the tagged format this replaced came back to an old gateway as a string).
func TestEncodeStoredValue_readByAPreviousGateway(t *testing.T) {
	previousGatewayRead := func(b []byte) any {
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			return string(b)
		}
		return v
	}
	for _, v := range []any{"123", "true", "hello", float64(123), true, nil,
		map[string]any{"a": float64(1)}, []any{"x", float64(2)}} {
		b, err := encodeStoredValue(v)
		if err != nil {
			t.Fatal(err)
		}
		if got := previousGatewayRead(b); !reflect.DeepEqual(got, v) {
			t.Errorf("%#v: a previous gateway reads %#v", v, got)
		}
	}
}
