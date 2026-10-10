//go:build e2e_fleet

package cache

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	// manyDMaps is far more dmaps than the 2G MemoryMax allows at one 256 MiB
	// bound each (eight): a namespace's cache is one Olric DMap, so the count
	// of tenant dmaps does not multiply the bound.
	manyDMaps = 40
	// keyLimit is Olric's key limit, shared by a dmap name's prefix and the key.
	keyLimit = 255
)

// TestCache_manyDMapsStayApart: dmaps are folded into the namespace's one
// Olric DMap, so many of them must each still hold their own keys and list only
// those. (The memory bound they share is watched by the soak, cacheCeilingMB.)
func TestCache_manyDMapsStayApart(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for d := range manyDMaps {
		dmap := fmt.Sprintf("many-%d", d)
		for k := range 3 {
			put(t, n.Client, tenancy.Owner(n), dmap, fmt.Sprintf("k%d", k), fmt.Sprintf("%s/%d", dmap, k), "").Expect(t, http.StatusOK)
		}
	}
	for d := range manyDMaps {
		dmap := fmt.Sprintf("many-%d", d)
		if got, want := scanKeys(t, n, dmap, ""), []string{"k0", "k1", "k2"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("scan %s = %v, want %v", dmap, got, want)
		}
		if got, want := mustGet(t, n.Client, tenancy.Owner(n), dmap, "k1"), dmap+"/1"; got != want {
			t.Fatalf("%s/k1 read %v, want %q", dmap, got, want)
		}
	}
	// A dmap whose name begins like another's keeps its own keys.
	put(t, n.Client, tenancy.Owner(n), "many-1", "k0-in-1", 1, "").Expect(t, http.StatusOK)
	if got := scanKeys(t, n, "many-10", ""); len(got) != 3 {
		t.Fatalf("scan many-10 = %v after a write to many-1", got)
	}
}

// TestCache_keyLimitSharesBytesWithTheDMapName: a dmap name and its key share
// Olric's 255 bytes, so the longest key shrinks with the name; the 413 states
// the limit that applies (website/src/docs/developer/sdk-reference.mdx "Cache").
func TestCache_keyLimitSharesBytesWithTheDMapName(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	const dmap = "sessions"
	longest := keyLimit - len("8:sessions")
	put(t, n.Client, tenancy.Owner(n), dmap, strings.Repeat("k", longest), 1, "").Expect(t, http.StatusOK)
	resp := put(t, n.Client, tenancy.Owner(n), dmap, strings.Repeat("k", longest+1), 1, "").Expect(t, http.StatusRequestEntityTooLarge)
	if !strings.Contains(string(resp.Body), fmt.Sprint(longest)) {
		t.Errorf("the refusal does not name the %d-byte limit: %s", longest, resp.Body)
	}
	get(t, n.Client, tenancy.Owner(n), dmap, strings.Repeat("k", longest+1)).Expect(t, http.StatusNotFound)
}
