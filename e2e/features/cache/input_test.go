//go:build e2e_fleet

package cache

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	// bodyLimit is every cache handler's MaxBytesReader (handlers/cache).
	bodyLimit = 10 << 20
	// largeValue is well under the limit and well over any packet.
	largeValue = 2 << 20
	// writers is how many goroutines race on one key.
	writers = 16
)

func TestCacheInput_malformedRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	cases := map[string][]byte{
		"not json":      []byte("dmap=m&key=k"),
		"truncated":     []byte(`{"dmap":"m","key":`),
		"missing dmap":  []byte(`{"key":"k","value":1}`),
		"missing key":   []byte(`{"dmap":"m","value":1}`),
		"blank key":     []byte(`{"dmap":"m","key":"   ","value":1}`),
		"missing value": []byte(`{"dmap":"m","key":"k"}`),
		"null value":    []byte(`{"dmap":"m","key":"k","value":null}`),
		"key not text":  []byte(`{"dmap":"m","key":5,"value":1}`),
		"array body":    []byte(`[{"dmap":"m","key":"k","value":1}]`),
	}
	for name, body := range cases {
		if resp := tenancy.Post(t, n.Client, pathPut, tenancy.Owner(n), body); resp.Status != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d: %s", name, resp.Status, resp.Body)
		}
	}
	if keys := scanKeys(t, n, "m", ""); len(keys) != 0 {
		t.Fatalf("a refused put stored %v", keys)
	}
}

func TestCacheInput_wrongMethodRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for path := range cacheBodies {
		if resp := tenancy.Send(t, n.Client, http.MethodGet, path, tenancy.Owner(n), nil); resp.Status != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: want 405, got %d", path, resp.Status)
		}
	}
}

// TestCacheInput_largeValues: a 2 MiB value round-trips; a body over the
// 10 MiB limit is refused, not stored and not a 5xx.
func TestCacheInput_largeValues(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	big := strings.Repeat("0123456789abcdef", largeValue/16)
	put(t, n.Client, tenancy.Owner(n), "big", "fits", big, "").Expect(t, http.StatusOK)
	if got := mustGet(t, n.Client, tenancy.Owner(n), "big", "fits"); got != big {
		t.Fatalf("the %d-byte value came back as %d bytes", len(big), len(fmt.Sprint(got)))
	}
	huge := strings.Repeat("x", bodyLimit+1)
	resp := put(t, n.Client, tenancy.Owner(n), "big", "over", huge, "")
	if resp.Status != http.StatusBadRequest && resp.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("an over-limit body answered %d, want 400/413", resp.Status)
	}
	get(t, n.Client, tenancy.Owner(n), "big", "over").Expect(t, http.StatusNotFound)
}

// TestCacheInput_hostileKeys: keys are opaque. Traversal, separators, unicode
// and a long key are stored and read back under exactly that key.
func TestCacheInput_hostileKeys(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	keys := []string{"../../etc/passwd", "a/b/../c", "k:with:colons", "' OR 1=1 --", "\u202eevil", "ключ 鍵",
		"é", strings.Repeat("k", 4096), "%00", "*", ".*"}
	for i, k := range keys {
		resp := put(t, n.Client, tenancy.Owner(n), "hostile", k, i, "")
		if resp.Status >= http.StatusInternalServerError {
			t.Errorf("key %q broke put: %d %s", k, resp.Status, resp.Body)
			continue
		}
		if resp.Status == http.StatusOK {
			if got := mustGet(t, n.Client, tenancy.Owner(n), "hostile", k); got != float64(i) {
				t.Errorf("key %q read back %v, want %d", k, got, i)
			}
		}
	}
	// A regex-looking map name stays inside this namespace's prefix.
	put(t, n.Client, tenancy.Owner(n), ".*", "k", "v", "").Expect(t, http.StatusOK)
	if got := mustGet(t, n.Client, tenancy.Owner(n), ".*", "k"); got != "v" {
		t.Fatalf("map \".*\" read %v", got)
	}
}

// TestCacheInput_duplicateJSONKeys: Go's decoder keeps the last member, so the
// value lands under exactly one key and nothing else is written.
func TestCacheInput_duplicateJSONKeys(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	tenancy.Post(t, n.Client, pathPut, tenancy.Owner(n), []byte(`{"dmap":"m","key":"first","key":"last","value":1}`)).Expect(t, http.StatusOK)
	get(t, n.Client, tenancy.Owner(n), "m", "first").Expect(t, http.StatusNotFound)
	mustGet(t, n.Client, tenancy.Owner(n), "m", "last")
}

// TestCacheConcurrency_lastWriterWins: writers race on one key; the key ends
// holding one of the written values, and every distinct key written in
// parallel is present.
func TestCacheConcurrency_lastWriterWins(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	c := n.Client.For(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2*writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, key := range []string{"shared", fmt.Sprintf("own-%d", i)} {
				resp, err := c.JSON(t.Context(), http.MethodPost, pathPut, n.Owner.Token(),
					map[string]any{"dmap": "race", "key": key, "value": float64(i)}, nil)
				if err != nil {
					errs <- fmt.Errorf("writer %d, %s: %w (%v)", i, key, err, statusOf(resp))
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got, ok := mustGet(t, n.Client, tenancy.Owner(n), "race", "shared").(float64)
	if !ok || got < 0 || got >= writers {
		t.Fatalf("the raced key holds %v, which no writer wrote", got)
	}
	if keys := scanKeys(t, n, "race", "^own-"); len(keys) != writers {
		t.Fatalf("%d of %d parallel keys are present: %v", len(keys), writers, keys)
	}
}

func statusOf(resp *gw.Response) any {
	if resp == nil {
		return "no response"
	}
	return resp.Status
}
