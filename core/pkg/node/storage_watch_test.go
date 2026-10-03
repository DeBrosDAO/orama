package node

import (
	"slices"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/node/boot"
)

// The storage units are watched after boot, without an IPFS outage blocking
// what depends on storage: the health check sits on a watchdog nothing
// depends on, never on storage itself.
func TestBootComponents_storageIsWatchedWithoutBlockingItsDependents(t *testing.T) {
	components := mustBootComponents(t, newGraphNode(t))
	byName := map[string]boot.Component{}
	for _, c := range components {
		byName[c.Name] = c
	}
	watch, ok := byName[compStorageWatch]
	if !ok {
		t.Fatalf("no %q component: a storage unit that goes down after boot is never started again", compStorageWatch)
	}
	if watch.Health == nil || watch.Reconcile == nil {
		t.Fatalf("%q needs both a health check and a reconcile", compStorageWatch)
	}
	if !slices.Contains(watch.DependsOn, compStorage) {
		t.Errorf("%q must wait for storage's first start, depends on %v", compStorageWatch, watch.DependsOn)
	}
	if byName[compStorage].Health != nil {
		t.Errorf("%q has a health check: its failure would block rqlite-local and the gateway", compStorage)
	}
	for _, c := range components {
		if slices.Contains(c.DependsOn, compStorageWatch) {
			t.Errorf("%q depends on %q: a storage outage would block it", c.Name, compStorageWatch)
		}
	}
}
