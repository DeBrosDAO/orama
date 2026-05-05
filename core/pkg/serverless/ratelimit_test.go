package serverless

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMultiTier_within_limit_allows(t *testing.T) {
	l := NewMultiTierLimiter(DefaultLimiterConfig())
	for i := 0; i < 10; i++ {
		d, _ := l.AllowRequest(context.Background(), RateLimitRequest{
			Namespace: "ns", Function: "fn", Wallet: "w1",
		})
		if !d.Allowed {
			t.Fatalf("request %d unexpectedly denied", i)
		}
	}
}

func TestMultiTier_per_wallet_burst_exhausted(t *testing.T) {
	cfg := DefaultLimiterConfig()
	cfg.PerWalletBurst = 5
	cfg.PerWalletPerMinute = 600 // refill 10/sec, slow enough that burst matters
	l := NewMultiTierLimiter(cfg)

	// Burn the burst.
	for i := 0; i < 5; i++ {
		d, _ := l.AllowRequest(context.Background(), RateLimitRequest{
			Namespace: "ns", Wallet: "w1",
		})
		if !d.Allowed {
			t.Fatalf("burst[%d] should be allowed", i)
		}
	}
	// Next one rejected.
	d, _ := l.AllowRequest(context.Background(), RateLimitRequest{
		Namespace: "ns", Wallet: "w1",
	})
	if d.Allowed {
		t.Fatal("expected rejection after burst")
	}
	if d.Scope != "per_wallet" {
		t.Errorf("expected scope=per_wallet, got %q", d.Scope)
	}
	if d.RetryAfter <= 0 {
		t.Errorf("expected positive RetryAfter, got %v", d.RetryAfter)
	}
}

func TestMultiTier_per_wallet_isolation(t *testing.T) {
	cfg := DefaultLimiterConfig()
	cfg.PerWalletBurst = 3
	cfg.PerWalletPerMinute = 60 // 1/sec — slow refill
	l := NewMultiTierLimiter(cfg)

	// Wallet A burns its burst.
	for i := 0; i < 3; i++ {
		d, _ := l.AllowRequest(context.Background(), RateLimitRequest{
			Namespace: "ns", Wallet: "A",
		})
		if !d.Allowed {
			t.Fatalf("A[%d] should be allowed", i)
		}
	}
	dA, _ := l.AllowRequest(context.Background(), RateLimitRequest{
		Namespace: "ns", Wallet: "A",
	})
	if dA.Allowed {
		t.Fatal("expected A to be rate-limited")
	}

	// Wallet B unaffected.
	dB, _ := l.AllowRequest(context.Background(), RateLimitRequest{
		Namespace: "ns", Wallet: "B",
	})
	if !dB.Allowed {
		t.Fatal("expected B to be allowed (isolated from A)")
	}
}

func TestMultiTier_per_namespace_ceiling(t *testing.T) {
	cfg := DefaultLimiterConfig()
	cfg.PerNamespaceBurst = 3
	cfg.PerNamespacePerMinute = 60
	cfg.PerWalletBurst = 1000 // way above ns burst
	cfg.PerWalletPerMinute = 60_000
	l := NewMultiTierLimiter(cfg)

	// Different wallets, but they share the namespace ceiling.
	for i := 0; i < 3; i++ {
		d, _ := l.AllowRequest(context.Background(), RateLimitRequest{
			Namespace: "ns",
			Wallet:    "w" + string(rune('1'+i)),
		})
		if !d.Allowed {
			t.Fatalf("ns burst[%d] should be allowed", i)
		}
	}
	d, _ := l.AllowRequest(context.Background(), RateLimitRequest{
		Namespace: "ns", Wallet: "w99",
	})
	if d.Allowed {
		t.Fatal("expected per-namespace ceiling rejection")
	}
	if d.Scope != "per_namespace" {
		t.Errorf("expected scope=per_namespace, got %q", d.Scope)
	}
}

func TestMultiTier_per_function_override_tighter(t *testing.T) {
	cfg := DefaultLimiterConfig() // per-wallet burst 60
	l := NewMultiTierLimiter(cfg)

	override := &PerFunctionRateLimit{
		PerWalletPerMinute: 60, // 1/sec
		PerWalletBurst:     2,
	}
	for i := 0; i < 2; i++ {
		d, _ := l.AllowRequest(context.Background(), RateLimitRequest{
			Namespace: "ns", Function: "expensive", Wallet: "w1",
			Override: override,
		})
		if !d.Allowed {
			t.Fatalf("override burst[%d] should allow", i)
		}
	}
	d, _ := l.AllowRequest(context.Background(), RateLimitRequest{
		Namespace: "ns", Function: "expensive", Wallet: "w1",
		Override: override,
	})
	if d.Allowed {
		t.Fatal("expected override to reject")
	}
	if d.Scope != "per_function_wallet" {
		t.Errorf("expected scope=per_function_wallet, got %q", d.Scope)
	}
}

func TestMultiTier_anonymous_falls_back_to_ip(t *testing.T) {
	cfg := DefaultLimiterConfig()
	cfg.PerIPBurst = 2
	cfg.PerIPPerMinute = 60
	l := NewMultiTierLimiter(cfg)

	for i := 0; i < 2; i++ {
		d, _ := l.AllowRequest(context.Background(), RateLimitRequest{
			Namespace: "ns", IP: "1.2.3.4",
		})
		if !d.Allowed {
			t.Fatalf("IP burst[%d] should allow", i)
		}
	}
	d, _ := l.AllowRequest(context.Background(), RateLimitRequest{
		Namespace: "ns", IP: "1.2.3.4",
	})
	if d.Allowed {
		t.Fatal("expected per-IP rejection for anonymous caller")
	}
	if d.Scope != "per_ip" {
		t.Errorf("expected scope=per_ip, got %q", d.Scope)
	}

	// Different IP unaffected.
	d2, _ := l.AllowRequest(context.Background(), RateLimitRequest{
		Namespace: "ns", IP: "5.6.7.8",
	})
	if !d2.Allowed {
		t.Fatal("expected different IP to be allowed")
	}
}

func TestMultiTier_lru_eviction_when_cap_reached(t *testing.T) {
	cfg := DefaultLimiterConfig()
	cfg.PerWalletBurst = 1
	cfg.PerWalletPerMinute = 60
	cfg.MaxBucketsPerScope = 32 // 2 per shard with 16 shards
	l := NewMultiTierLimiter(cfg)

	// Saturate one wallet's bucket so its retry-after > 0.
	l.AllowRequest(context.Background(), RateLimitRequest{Namespace: "ns", Wallet: "victim"})
	d, _ := l.AllowRequest(context.Background(), RateLimitRequest{Namespace: "ns", Wallet: "victim"})
	if d.Allowed {
		t.Fatal("victim should be rate-limited initially")
	}

	// Push lots of new wallets through to evict the victim.
	// We need many to ensure same-shard collisions.
	for i := 0; i < 1000; i++ {
		l.AllowRequest(context.Background(), RateLimitRequest{
			Namespace: "ns", Wallet: "filler-" + string(rune(i%256)) + "-" + string(rune(i/256)),
		})
	}

	// After eviction, victim's bucket is recreated full → first call allowed again.
	// (We can't deterministically assert eviction without inspecting internals;
	// the test confirms LRU growth doesn't blow up — no panic, no deadlock.)
	_, err := l.AllowRequest(context.Background(), RateLimitRequest{
		Namespace: "ns", Wallet: "victim",
	})
	if err != nil {
		t.Errorf("unexpected error after LRU churn: %v", err)
	}
}

func TestMultiTier_concurrent_no_race(t *testing.T) {
	// Run with -race
	l := NewMultiTierLimiter(DefaultLimiterConfig())
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				l.AllowRequest(context.Background(), RateLimitRequest{
					Namespace: "ns",
					Function:  "fn",
					Wallet:    "w" + string(rune(id)),
					IP:        "1.2.3.4",
				})
			}
		}(g)
	}
	wg.Wait()
}

func TestMultiTier_satisfies_legacy_interface(t *testing.T) {
	var _ RateLimiter = (*MultiTierLimiter)(nil)
	var _ TieredRateLimiter = (*MultiTierLimiter)(nil)
}

func TestRateLimitedError_message(t *testing.T) {
	e := &RateLimitedError{Scope: "per_wallet", RetryAfter: 2 * time.Second}
	if e.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

// Sanity-check the legacy TokenBucketLimiter still works for any code on the
// old single-bucket path.
func TestTokenBucketLimiter_legacy_works(t *testing.T) {
	l := NewTokenBucketLimiter(60)
	allowed, _ := l.Allow(context.Background(), "global")
	if !allowed {
		t.Error("first call should be allowed")
	}
}
