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

func TestCacheIncrBy_concurrentIncrementsAcrossMembersAreAllCounted(t *testing.T) {
	members := olrictest.StartCluster(t, 3)
	const workers = 20
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(m *olrictest.Server) {
			defer wg.Done()
			h := &HostFunctions{cacheClient: fixedOlric(clusterClient(t, m))}
			if _, err := h.CacheIncr(nsCtx(), "counter"); err != nil {
				t.Errorf("CacheIncr: %v", err)
			}
		}(members[i%len(members)])
	}
	wg.Wait()

	h := &HostFunctions{cacheClient: fixedOlric(clusterClient(t, members[0]))}
	got, err := h.CacheIncrBy(nsCtx(), "counter", 0)
	if err != nil || got != workers {
		t.Errorf("%d concurrent increments across 3 members left %d (err %v)", workers, got, err)
	}
}

// fixedOlric is a provider that always yields c.
func fixedOlric(c olriclib.Client) func() olriclib.Client { return func() olriclib.Client { return c } }
