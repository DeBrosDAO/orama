package cache

import (
	"net/http"
	"strings"
	"testing"
)

// Bug (stagenet e2e TestCacheInput_largeValues, _hostileKeys): a 2 MiB value
// and a 4096-byte key were answered 500 "cache unavailable", because Olric's
// refusal reaches the gateway as an error its client does not recognise. The
// gateway refuses them itself, as a client error, before the put.
func TestSetHandler_limitsAreEnforcedBeforeOlric(t *testing.T) {
	h, _ := handlersWithOlric(t)
	cases := []struct {
		name string
		req  PutRequest
		want int
	}{
		{"value over a table", PutRequest{DMap: "lim", Key: "k", Value: strings.Repeat("x", 2<<20)}, http.StatusRequestEntityTooLarge},
		{"key of 4096 bytes", PutRequest{DMap: "lim", Key: strings.Repeat("k", 4096), Value: 1}, http.StatusRequestEntityTooLarge},
		{"key one byte over", PutRequest{DMap: "lim", Key: strings.Repeat("k", MaxKeyBytes+1), Value: 1}, http.StatusRequestEntityTooLarge},
		{"longest key", PutRequest{DMap: "lim", Key: strings.Repeat("k", MaxKeyBytes), Value: 1}, http.StatusOK},
		{"multi-byte key over in bytes, under in characters", PutRequest{DMap: "lim", Key: strings.Repeat("é", 128), Value: 1}, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := put(t, h, c.req)
			if rec.Code != c.want {
				t.Fatalf("status %d, want %d: %.200s", rec.Code, c.want, rec.Body.String())
			}
		})
	}
	if got := postJSON(t, h.GetHandler, "/v1/cache/get", GetRequest{DMap: "lim", Key: "k"}); got.Code != http.StatusNotFound {
		t.Errorf("a refused value was stored: get answered %d", got.Code)
	}
}

// The limit is the one Olric enforces: the largest entry the boundary admits is
// stored, and one byte more is refused by the boundary.
func TestSetHandler_theLargestEntryATableHoldsIsStored(t *testing.T) {
	h, _ := handlersWithOlric(t)
	const key = "edge"
	// The stored value is the JSON string: the text and two quotes.
	text := OlricTableSizeBytes - olricEntryOverheadBytes - len(key) - 1 - 2
	value := strings.Repeat("v", text)
	if rec := put(t, h, PutRequest{DMap: "edge", Key: key, Value: value}); rec.Code != http.StatusOK {
		t.Fatalf("the largest entry that fits: status %d: %.200s", rec.Code, rec.Body.String())
	}
	if got := getValue(t, h, "edge", key); got != value {
		t.Fatalf("the largest entry came back as %d bytes, want %d", len(got.(string)), len(value))
	}
	if rec := put(t, h, PutRequest{DMap: "edge", Key: key, Value: value + "v"}); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("one byte past the largest entry: status %d, want 413", rec.Code)
	}
}

func TestEntryFitsTable(t *testing.T) {
	fill := OlricTableSizeBytes - olricEntryOverheadBytes
	cases := []struct {
		name     string
		key      string
		stored   int
		wantFits bool
	}{
		{"empty", "", 0, true},
		{"small", "k", 10, true},
		{"one byte under a table", "k", fill - 1 - 1, true},
		{"exactly a table", "k", fill - 1, false},
		{"over a table", "k", fill, false},
	}
	for _, c := range cases {
		if got := entryFitsTable(c.key, make([]byte, c.stored)); got != c.wantFits {
			t.Errorf("%s: entryFitsTable = %v, want %v", c.name, got, c.wantFits)
		}
	}
}
