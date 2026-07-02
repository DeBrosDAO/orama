package gateway

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Bugboard #858 — the fan-out resolver turns active dns_nodes into ntfy publish
// base URLs and caches them for a short TTL. These pin the transform + caching.

func TestNtfyFanoutResolver_buildsSchemeHostPort(t *testing.T) {
	r := &ntfyFanoutResolver{
		scheme: "https",
		port:   "",
		ttl:    time.Minute,
		query:  func(context.Context) ([]string, error) { return []string{"1.2.3.4", "5.6.7.8"}, nil },
	}
	hosts, err := r.Hosts(context.Background())
	if err != nil {
		t.Fatalf("Hosts: %v", err)
	}
	want := []string{"https://1.2.3.4", "https://5.6.7.8"}
	if len(hosts) != len(want) {
		t.Fatalf("got %v; want %v", hosts, want)
	}
	for i := range want {
		if hosts[i] != want[i] {
			t.Errorf("host[%d] = %q; want %q", i, hosts[i], want[i])
		}
	}
}

func TestNtfyFanoutResolver_includesExplicitPort(t *testing.T) {
	r := &ntfyFanoutResolver{
		scheme: "http",
		port:   "8090",
		ttl:    time.Minute,
		query:  func(context.Context) ([]string, error) { return []string{"10.0.0.6"}, nil },
	}
	hosts, _ := r.Hosts(context.Background())
	if len(hosts) != 1 || hosts[0] != "http://10.0.0.6:8090" {
		t.Errorf("got %v; want [http://10.0.0.6:8090]", hosts)
	}
}

func TestNtfyFanoutResolver_skipsEmptyIPs(t *testing.T) {
	r := &ntfyFanoutResolver{
		scheme: "https",
		ttl:    time.Minute,
		query:  func(context.Context) ([]string, error) { return []string{"", "1.2.3.4", ""}, nil },
	}
	hosts, _ := r.Hosts(context.Background())
	if len(hosts) != 1 || hosts[0] != "https://1.2.3.4" {
		t.Errorf("got %v; want only the non-empty IP", hosts)
	}
}

func TestNtfyFanoutResolver_cachesWithinTTL(t *testing.T) {
	calls := 0
	r := &ntfyFanoutResolver{
		scheme: "https",
		ttl:    time.Minute,
		query: func(context.Context) ([]string, error) {
			calls++
			return []string{"1.2.3.4"}, nil
		},
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Hosts(context.Background()); err != nil {
			t.Fatalf("Hosts: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("query called %d times; want 1 (cached within TTL)", calls)
	}
}

func TestNtfyFanoutResolver_requeriesAfterTTL(t *testing.T) {
	calls := 0
	r := &ntfyFanoutResolver{
		scheme: "https",
		ttl:    time.Nanosecond, // expire immediately
		query: func(context.Context) ([]string, error) {
			calls++
			return []string{"1.2.3.4"}, nil
		},
	}
	_, _ = r.Hosts(context.Background())
	time.Sleep(time.Millisecond)
	_, _ = r.Hosts(context.Background())
	if calls != 2 {
		t.Errorf("query called %d times; want 2 (TTL expired between calls)", calls)
	}
}

func TestNtfyFanoutResolver_queryError_returnsStaleCache(t *testing.T) {
	fail := false
	r := &ntfyFanoutResolver{
		scheme: "https",
		ttl:    time.Nanosecond,
		query: func(context.Context) ([]string, error) {
			if fail {
				return nil, errors.New("rqlite unreachable")
			}
			return []string{"1.2.3.4"}, nil
		},
	}
	// Prime the cache.
	if _, err := r.Hosts(context.Background()); err != nil {
		t.Fatalf("prime: %v", err)
	}
	time.Sleep(time.Millisecond)
	// Now the query fails — Hosts must return the stale cache alongside the error
	// so the caller can fall back rather than drop the push.
	fail = true
	hosts, err := r.Hosts(context.Background())
	if err == nil {
		t.Fatal("want the query error surfaced")
	}
	if len(hosts) != 1 || hosts[0] != "https://1.2.3.4" {
		t.Errorf("want the stale cache returned on error; got %v", hosts)
	}
}
