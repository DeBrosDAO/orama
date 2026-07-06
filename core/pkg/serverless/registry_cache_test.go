package serverless

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// Bugboard #708 — function metadata + env vars are cached in-process so a burst
// of invokes doesn't pay a leader-routed weak read per op. These pin the cache
// hit/miss/TTL/invalidation behavior and the dedup'd authorization decision.

func newTestRegistry() *Registry {
	return NewRegistry(NewMockRQLite(), NewMockIPFSClient(), RegistryConfig{}, zap.NewNop())
}

func TestRegistryCache_hitAndInvalidate(t *testing.T) {
	r := newTestRegistry()
	key := fnCacheKey("ns", "fn", 0)
	fn := &Function{ID: "id-1", Name: "fn", Namespace: "ns"}

	if _, ok := r.cachedFn(key); ok {
		t.Fatal("empty cache must miss")
	}
	r.storeFn(key, fn)
	got, ok := r.cachedFn(key)
	if !ok || got != fn {
		t.Fatalf("expected cache hit returning the stored fn; ok=%v got=%v", ok, got)
	}

	// Deploy/enable/disable/delete must drop every cached version.
	r.storeFn(fnCacheKey("ns", "fn", 3), &Function{ID: "id-3", Name: "fn", Namespace: "ns"})
	r.invalidateFn("ns", "fn")
	if _, ok := r.cachedFn(key); ok {
		t.Error("invalidateFn must drop the version-0 entry")
	}
	if _, ok := r.cachedFn(fnCacheKey("ns", "fn", 3)); ok {
		t.Error("invalidateFn must drop ALL versions of the function")
	}
}

func TestRegistryCache_invalidateScopedToFunction(t *testing.T) {
	r := newTestRegistry()
	r.storeFn(fnCacheKey("ns", "keep", 0), &Function{ID: "k", Name: "keep", Namespace: "ns"})
	r.storeFn(fnCacheKey("ns", "drop", 0), &Function{ID: "d", Name: "drop", Namespace: "ns"})

	r.invalidateFn("ns", "drop")

	if _, ok := r.cachedFn(fnCacheKey("ns", "drop", 0)); ok {
		t.Error("target function must be invalidated")
	}
	if _, ok := r.cachedFn(fnCacheKey("ns", "keep", 0)); !ok {
		t.Error("a DIFFERENT function must NOT be invalidated (prefix must include the null separator)")
	}
}

func TestRegistryCache_ttlExpiry(t *testing.T) {
	r := newTestRegistry()
	key := fnCacheKey("ns", "fn", 0)
	// Backdate the entry beyond the TTL.
	r.fnCache[key] = fnCacheEntry{fn: &Function{ID: "x"}, at: time.Now().Add(-2 * r.cacheTTL)}
	if _, ok := r.cachedFn(key); ok {
		t.Error("an entry older than the TTL must be treated as a miss")
	}
}

func TestRegistryCache_envHitAndTTL(t *testing.T) {
	r := newTestRegistry()
	if _, ok := r.cachedEnv("fid"); ok {
		t.Fatal("empty env cache must miss")
	}
	r.storeEnv("fid", map[string]string{"K": "V"})
	if env, ok := r.cachedEnv("fid"); !ok || env["K"] != "V" {
		t.Fatalf("expected env cache hit; ok=%v env=%v", ok, env)
	}
	r.envCache["fid"] = envCacheEntry{env: map[string]string{"K": "V"}, at: time.Now().Add(-2 * r.cacheTTL)}
	if _, ok := r.cachedEnv("fid"); ok {
		t.Error("env entry older than the TTL must miss")
	}
}

func TestRegistryCache_envInvalidatedOnRedeploy(t *testing.T) {
	// A redeploy REUSES the function ID (Register: id = oldFn.ID) and rewrites
	// env vars under it, so Register must drop the env cache for that ID — else
	// a changed env var (e.g. a rotated endpoint) is masked for up to the TTL.
	r := newTestRegistry()
	r.storeEnv("fid", map[string]string{"K": "old"})
	if env, ok := r.cachedEnv("fid"); !ok || env["K"] != "old" {
		t.Fatal("precondition: env should be cached")
	}
	r.invalidateEnv("fid") // what Register now calls
	if _, ok := r.cachedEnv("fid"); ok {
		t.Error("env cache must be invalidated on redeploy (reused ID); a changed env var must not be served stale")
	}
}

func TestRegistryCache_keyDistinctNoCollision(t *testing.T) {
	// Guard the null-separated key: "a"+"bc" must not collide with "ab"+"c".
	if fnCacheKey("a", "bc", 0) == fnCacheKey("ab", "c", 0) {
		t.Error("cache keys must not collide across namespace/name boundaries")
	}
}

func TestCanInvokeFn(t *testing.T) {
	if !canInvokeFn(&Function{IsPublic: true}, "", false) {
		t.Error("public function must be invokable by an anonymous caller")
	}
	if canInvokeFn(&Function{IsPublic: false}, "", false) {
		t.Error("private function must reject an empty (anonymous) caller")
	}
	if canInvokeFn(&Function{IsPublic: false}, "   ", false) {
		t.Error("private function must reject a whitespace-only caller")
	}
	if !canInvokeFn(&Function{IsPublic: false}, "wallet-abc", false) {
		t.Error("private function must accept an identified caller")
	}
}

// TestRowToFunction_isInternal guards the read path enforcement depends on
// (bugboard #152): if rowToFunction drops is_internal, fn.IsInternal reads
// false for every function and the gate silently never engages. Asserts the
// scan struct field maps through to the Function struct in both directions.
func TestRowToFunction_isInternal(t *testing.T) {
	r := &Registry{}
	if got := r.rowToFunction(&functionRow{IsInternal: true}); !got.IsInternal {
		t.Error("rowToFunction must carry IsInternal=true through to the Function")
	}
	if got := r.rowToFunction(&functionRow{IsInternal: false}); got.IsInternal {
		t.Error("rowToFunction must carry IsInternal=false through to the Function")
	}
}

// TestCanInvokeFn_internal exercises the bugboard #152 gate: an internal
// function is invokable ONLY by an admin caller (system triggers bypass
// canInvokeFn entirely via isSystemTrigger, so reaching here means an
// external caller — admin required). Non-internal behavior is unchanged.
func TestCanInvokeFn_internal(t *testing.T) {
	tests := []struct {
		name          string
		fn            *Function
		callerWallet  string
		callerIsAdmin bool
		want          bool
	}{
		{
			name:          "internal private, admin caller allowed",
			fn:            &Function{IsInternal: true, IsPublic: false},
			callerWallet:  "wallet-abc",
			callerIsAdmin: true,
			want:          true,
		},
		{
			name:          "internal private, non-admin identified caller denied",
			fn:            &Function{IsInternal: true, IsPublic: false},
			callerWallet:  "wallet-abc",
			callerIsAdmin: false,
			want:          false,
		},
		{
			name:          "internal public, non-admin caller still denied",
			fn:            &Function{IsInternal: true, IsPublic: true},
			callerWallet:  "wallet-abc",
			callerIsAdmin: false,
			want:          false,
		},
		{
			name:          "internal public, admin caller allowed",
			fn:            &Function{IsInternal: true, IsPublic: true},
			callerWallet:  "",
			callerIsAdmin: true,
			want:          true,
		},
		{
			name:          "non-internal public, anyone allowed (unchanged)",
			fn:            &Function{IsInternal: false, IsPublic: true},
			callerWallet:  "",
			callerIsAdmin: false,
			want:          true,
		},
		{
			name:          "non-internal private, identified caller allowed (unchanged)",
			fn:            &Function{IsInternal: false, IsPublic: false},
			callerWallet:  "wallet-abc",
			callerIsAdmin: false,
			want:          true,
		},
		{
			name:          "non-internal private, anonymous caller denied (unchanged)",
			fn:            &Function{IsInternal: false, IsPublic: false},
			callerWallet:  "",
			callerIsAdmin: false,
			want:          false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canInvokeFn(tt.fn, tt.callerWallet, tt.callerIsAdmin); got != tt.want {
				t.Errorf("canInvokeFn = %v, want %v", got, tt.want)
			}
		})
	}
}
