package hostfunctions

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/olric/olrictest"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	olriclib "github.com/olric-data/olric"
)

// plainMissDMap answers a Get the way a gateway's cluster client does for a
// missing key: an error carrying the text "key not found" but not the olriclib
// sentinel, because that process never registered the server's error codes.
type plainMissDMap struct{ olriclib.DMap }

func (plainMissDMap) Get(context.Context, string) (*olriclib.GetResponse, error) {
	return nil, errors.New("key not found")
}

type plainMissClient struct{ olriclib.Client }

func (plainMissClient) NewDMap(string, ...olriclib.DMapOption) (olriclib.DMap, error) {
	return plainMissDMap{}, nil
}

func TestCacheGet_missFromClusterClientIsACacheMiss(t *testing.T) {
	h := &HostFunctions{cacheClient: fixedOlric(plainMissClient{})}
	_, err := h.CacheGet(nsCtx(), "absent")
	if !errors.Is(err, serverless.ErrCacheMiss) {
		t.Errorf("CacheGet of a missing key: err = %v, want ErrCacheMiss", err)
	}
}

// clusterClient is the kind of client a gateway hands the host functions: a
// cluster client on one member, which routes each key to its partition owner.
// An embedded client runs Incr under the local member's lock instead, so
// increments through different members would not exclude each other.
func clusterClient(t *testing.T, m *olrictest.Server) olriclib.Client {
	t.Helper()
	c, err := olriclib.NewClusterClient([]string{m.Addr})
	if err != nil {
		t.Fatalf("failed to create a cluster client on %s: %v", m.Addr, err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

// Every caller must see its own value: n atomic increments return 1..n each
// once. A lost update leaves the final count short, and two callers that read
// the same value before either wrote return a repeat, which the final count
// can hide when a later increment lands on top.
func TestCacheIncrBy_concurrentIncrementsAcrossMembersAreAllCounted(t *testing.T) {
	members := olrictest.StartCluster(t, 3)
	const workers = 60
	returned := make(chan int64, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(m *olrictest.Server) {
			defer wg.Done()
			h := &HostFunctions{cacheClient: fixedOlric(clusterClient(t, m))}
			v, err := h.CacheIncr(nsCtx(), "counter")
			if err != nil {
				t.Errorf("CacheIncr: %v", err)
				return
			}
			returned <- v
		}(members[i%len(members)])
	}
	wg.Wait()
	close(returned)

	seen := make(map[int64]int)
	for v := range returned {
		seen[v]++
	}
	for v := int64(1); v <= workers; v++ {
		if seen[v] != 1 {
			t.Errorf("value %d was returned %d times, want once (returned: %v)", v, seen[v], seen)
		}
	}

	h := &HostFunctions{cacheClient: fixedOlric(clusterClient(t, members[0]))}
	got, err := h.CacheIncrBy(nsCtx(), "counter", 0)
	if err != nil || got != workers {
		t.Errorf("%d concurrent increments across 3 members left %d (err %v)", workers, got, err)
	}
}

// Increments through different members land in one per-namespace counter, and
// two namespaces' counters under the same key stay apart.
func TestCacheIncrBy_oneCounterPerNamespaceAcrossMembers(t *testing.T) {
	members := olrictest.StartCluster(t, 2)
	a := &HostFunctions{cacheClient: fixedOlric(clusterClient(t, members[0]))}
	b := &HostFunctions{cacheClient: fixedOlric(clusterClient(t, members[1]))}
	other := invocationCtx(&serverless.InvocationContext{Namespace: "other"})

	for i, h := range []*HostFunctions{a, b, a, b} {
		got, err := h.CacheIncr(nsCtx(), "n")
		if err != nil || got != int64(i+1) {
			t.Fatalf("increment %d through member %d = %d, %v; want %d", i+1, i%2, got, err, i+1)
		}
	}
	if got, err := a.CacheIncr(other, "n"); err != nil || got != 1 {
		t.Fatalf("another namespace's counter under the same key = %d, %v; want 1", got, err)
	}
}

// fixedOlric is a provider that always yields c.
func fixedOlric(c olriclib.Client) func() olriclib.Client { return func() olriclib.Client { return c } }
