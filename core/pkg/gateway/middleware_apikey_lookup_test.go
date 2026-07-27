package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// fakeAPIKeyQuerier is a configurable apiKeyQuerier stand-in for exercising
// lookupAPIKeyEntry / lookupAPIKeyNamespace without a real database.
type fakeAPIKeyQuerier struct {
	queryFn func(ctx context.Context, sql string, args ...interface{}) (*client.QueryResult, error)
	calls   int
}

func (f *fakeAPIKeyQuerier) Query(ctx context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	f.calls++
	return f.queryFn(ctx, sql, args...)
}

// hashingGatewayFixture builds a Gateway with just enough wiring (an auth
// service with an HMAC secret set, so HashAPIKey(key) != key) to exercise
// lookupAPIKeyEntry's hashed/raw dual lookup.
func hashingGatewayFixture(t *testing.T) *Gateway {
	t.Helper()
	svc, err := gwauth.NewService(newRQLiteTestLogger(), nil, "", "default")
	if err != nil {
		t.Fatalf("failed to construct auth service: %v", err)
	}
	svc.SetAPIKeyHMACSecret("test-hmac-secret")
	return &Gateway{authService: svc}
}

func nsRow(ns, scopes string) *client.QueryResult {
	return &client.QueryResult{
		Columns: []string{"namespaces.name", "api_keys.scopes"},
		Rows:    [][]interface{}{{ns, scopes}},
		Count:   1,
	}
}

func TestLookupAPIKeyEntry_HashedKeyHappyPath(t *testing.T) {
	g := hashingGatewayFixture(t)
	const rawKey = "ak_live_abc123"
	hashed := g.authService.HashAPIKey(rawKey)
	if hashed == rawKey {
		t.Fatal("precondition: HMAC secret must make hashed key differ from raw key")
	}

	q := &fakeAPIKeyQuerier{queryFn: func(_ context.Context, _ string, args ...interface{}) (*client.QueryResult, error) {
		if len(args) > 0 {
			if k, _ := args[0].(string); k == hashed {
				return nsRow("vrf708", "invoke,storage"), nil
			}
		}
		return &client.QueryResult{Count: 0}, nil
	}}

	ns, scopes, err := g.lookupAPIKeyEntry(context.Background(), rawKey, q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ns != "vrf708" {
		t.Errorf("expected namespace vrf708, got %q", ns)
	}
	if scopes != "invoke,storage" {
		t.Errorf("expected scopes 'invoke,storage', got %q", scopes)
	}
	if q.calls != 1 {
		t.Errorf("expected exactly 1 query (hashed lookup hit on first try), got %d", q.calls)
	}
}

func TestLookupAPIKeyEntry_RawKeyFallback(t *testing.T) {
	g := hashingGatewayFixture(t)
	const rawKey = "ak_legacy_unhashed"
	hashed := g.authService.HashAPIKey(rawKey)

	q := &fakeAPIKeyQuerier{queryFn: func(_ context.Context, _ string, args ...interface{}) (*client.QueryResult, error) {
		if len(args) > 0 {
			if k, _ := args[0].(string); k == rawKey && k != hashed {
				return nsRow("vrf708", ""), nil
			}
		}
		return &client.QueryResult{Count: 0}, nil
	}}

	ns, _, err := g.lookupAPIKeyEntry(context.Background(), rawKey, q)
	if err != nil {
		t.Fatalf("expected raw-key fallback to succeed, got error: %v", err)
	}
	if ns != "vrf708" {
		t.Errorf("expected namespace vrf708, got %q", ns)
	}
	if q.calls != 2 {
		t.Errorf("expected 2 queries (hashed miss, then raw-key fallback hit), got %d", q.calls)
	}
}

func TestLookupAPIKeyEntry_RevokedKeyRejected(t *testing.T) {
	g := hashingGatewayFixture(t)
	const rawKey = "ak_revoked_key"

	// The SQL filters `revoked_at IS NULL` — a revoked key's row is excluded
	// server-side and looks identical to "no such key" from the querier's
	// perspective. Assert the query text carries the filter and that a
	// revoked key resolves to the same "invalid API key" outcome as no rows.
	q := &fakeAPIKeyQuerier{queryFn: func(_ context.Context, sql string, _ ...interface{}) (*client.QueryResult, error) {
		if !strings.Contains(sql, "revoked_at IS NULL") {
			t.Errorf("expected query to filter revoked keys, got: %s", sql)
		}
		return &client.QueryResult{Count: 0}, nil
	}}

	_, _, err := g.lookupAPIKeyEntry(context.Background(), rawKey, q)
	if err == nil {
		t.Fatal("expected revoked key to be rejected, got nil error")
	}
	if !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("expected 'invalid API key' error, got: %v", err)
	}
}

func TestLookupAPIKeyEntry_NoRowsInvalid(t *testing.T) {
	g := hashingGatewayFixture(t)
	q := &fakeAPIKeyQuerier{queryFn: func(_ context.Context, _ string, _ ...interface{}) (*client.QueryResult, error) {
		return &client.QueryResult{Count: 0}, nil
	}}

	_, _, err := g.lookupAPIKeyEntry(context.Background(), "ak_does_not_exist", q)
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("expected 'invalid API key' error, got: %v", err)
	}
}

func TestLookupAPIKeyEntry_NilQuerier(t *testing.T) {
	g := hashingGatewayFixture(t)
	_, _, err := g.lookupAPIKeyEntry(context.Background(), "ak_whatever", nil)
	if err == nil {
		t.Fatal("expected error when querier is nil, got nil")
	}
}

func TestLookupAPIKeyEntry_DBErrorPropagates(t *testing.T) {
	g := hashingGatewayFixture(t)
	wantErr := fmt.Errorf("rqlite: leader not found")
	q := &fakeAPIKeyQuerier{queryFn: func(_ context.Context, _ string, _ ...interface{}) (*client.QueryResult, error) {
		return nil, wantErr
	}}

	_, _, err := g.lookupAPIKeyEntry(context.Background(), "ak_whatever", q)
	if err == nil {
		t.Fatal("expected DB error to propagate, got nil")
	}
	// A transient DB failure must be surfaced distinctly from "invalid API
	// key" — silently collapsing it would mask real outages as bad auth.
	if strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("DB error must not be reported as 'invalid API key', got: %v", err)
	}
	if !strings.Contains(err.Error(), wantErr.Error()) {
		t.Errorf("expected error to wrap underlying DB error %q, got: %v", wantErr, err)
	}
}

func TestLookupAPIKeyEntry_CacheHitBypassesQuery(t *testing.T) {
	g := hashingGatewayFixture(t)
	g.mwCache = newMiddlewareCache(time.Minute)
	g.mwCache.SetAPIKeyEntry("ak_cached", "cached-ns", "invoke")

	q := &fakeAPIKeyQuerier{queryFn: func(_ context.Context, _ string, _ ...interface{}) (*client.QueryResult, error) {
		t.Error("query must not run on a cache hit")
		return &client.QueryResult{Count: 0}, nil
	}}

	ns, scopes, err := g.lookupAPIKeyEntry(context.Background(), "ak_cached", q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ns != "cached-ns" || scopes != "invoke" {
		t.Errorf("expected cached (ns=cached-ns, scopes=invoke), got (ns=%s, scopes=%s)", ns, scopes)
	}
}

func TestLookupAPIKeyNamespace_WrapsEntry(t *testing.T) {
	g := hashingGatewayFixture(t)
	const rawKey = "ak_ns_only"
	hashed := g.authService.HashAPIKey(rawKey)

	q := &fakeAPIKeyQuerier{queryFn: func(_ context.Context, _ string, args ...interface{}) (*client.QueryResult, error) {
		if len(args) > 0 {
			if k, _ := args[0].(string); k == hashed {
				return nsRow("vrf708", ""), nil
			}
		}
		return &client.QueryResult{Count: 0}, nil
	}}

	ns, err := g.lookupAPIKeyNamespace(context.Background(), rawKey, q)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ns != "vrf708" {
		t.Errorf("expected namespace vrf708, got %q", ns)
	}
}
