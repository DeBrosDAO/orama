package gateway

import (
	"testing"
	"time"
)

func TestNewMiddlewareCache(t *testing.T) {
	t.Run("returns non-nil cache", func(t *testing.T) {
		mc := newMiddlewareCache(5 * time.Minute)
		defer mc.Stop()

		if mc == nil {
			t.Fatal("newMiddlewareCache() returned nil")
		}
	})

	t.Run("stop can be called without panic", func(t *testing.T) {
		mc := newMiddlewareCache(5 * time.Minute)
		// Should not panic
		mc.Stop()
	})
}

func TestAPIKeyNamespace(t *testing.T) {
	t.Run("set then get returns correct value", func(t *testing.T) {
		mc := newMiddlewareCache(5 * time.Minute)
		defer mc.Stop()

		mc.SetAPIKeyNamespace("key-abc", "my-namespace")

		got, ok := mc.GetAPIKeyNamespace("key-abc")
		if !ok {
			t.Fatal("expected ok=true, got false")
		}
		if got != "my-namespace" {
			t.Errorf("expected namespace %q, got %q", "my-namespace", got)
		}
	})

	t.Run("get non-existent key returns empty and false", func(t *testing.T) {
		mc := newMiddlewareCache(5 * time.Minute)
		defer mc.Stop()

		got, ok := mc.GetAPIKeyNamespace("nonexistent")
		if ok {
			t.Error("expected ok=false for non-existent key, got true")
		}
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("multiple keys stored independently", func(t *testing.T) {
		mc := newMiddlewareCache(5 * time.Minute)
		defer mc.Stop()

		mc.SetAPIKeyNamespace("key-1", "namespace-alpha")
		mc.SetAPIKeyNamespace("key-2", "namespace-beta")
		mc.SetAPIKeyNamespace("key-3", "namespace-gamma")

		tests := []struct {
			key  string
			want string
		}{
			{"key-1", "namespace-alpha"},
			{"key-2", "namespace-beta"},
			{"key-3", "namespace-gamma"},
		}

		for _, tt := range tests {
			t.Run(tt.key, func(t *testing.T) {
				got, ok := mc.GetAPIKeyNamespace(tt.key)
				if !ok {
					t.Fatalf("expected ok=true for key %q, got false", tt.key)
				}
				if got != tt.want {
					t.Errorf("key %q: expected %q, got %q", tt.key, tt.want, got)
				}
			})
		}
	})

	t.Run("overwriting a key updates the value", func(t *testing.T) {
		mc := newMiddlewareCache(5 * time.Minute)
		defer mc.Stop()

		mc.SetAPIKeyNamespace("key-x", "old-value")
		mc.SetAPIKeyNamespace("key-x", "new-value")

		got, ok := mc.GetAPIKeyNamespace("key-x")
		if !ok {
			t.Fatal("expected ok=true, got false")
		}
		if got != "new-value" {
			t.Errorf("expected %q, got %q", "new-value", got)
		}
	})
}

func TestNamespaceTargets(t *testing.T) {
	t.Run("set then get returns correct value", func(t *testing.T) {
		mc := newMiddlewareCache(5 * time.Minute)
		defer mc.Stop()

		targets := []gatewayTarget{
			{ip: "10.0.0.1", port: 8080},
			{ip: "10.0.0.2", port: 9090},
		}
		mc.SetNamespaceTargets("ns-web", targets)

		got, ok := mc.GetNamespaceTargets("ns-web")
		if !ok {
			t.Fatal("expected ok=true, got false")
		}
		if len(got) != len(targets) {
			t.Fatalf("expected %d targets, got %d", len(targets), len(got))
		}
		for i, tgt := range got {
			if tgt.ip != targets[i].ip || tgt.port != targets[i].port {
				t.Errorf("target[%d]: expected {%s %d}, got {%s %d}",
					i, targets[i].ip, targets[i].port, tgt.ip, tgt.port)
			}
		}
	})

	t.Run("get non-existent namespace returns nil and false", func(t *testing.T) {
		mc := newMiddlewareCache(5 * time.Minute)
		defer mc.Stop()

		got, ok := mc.GetNamespaceTargets("nonexistent")
		if ok {
			t.Error("expected ok=false for non-existent namespace, got true")
		}
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("multiple namespaces stored independently", func(t *testing.T) {
		mc := newMiddlewareCache(5 * time.Minute)
		defer mc.Stop()

		targets1 := []gatewayTarget{{ip: "1.1.1.1", port: 80}}
		targets2 := []gatewayTarget{{ip: "2.2.2.2", port: 443}, {ip: "3.3.3.3", port: 443}}

		mc.SetNamespaceTargets("ns-a", targets1)
		mc.SetNamespaceTargets("ns-b", targets2)

		got1, ok := mc.GetNamespaceTargets("ns-a")
		if !ok {
			t.Fatal("expected ok=true for ns-a")
		}
		if len(got1) != 1 || got1[0].ip != "1.1.1.1" {
			t.Errorf("ns-a: unexpected targets %v", got1)
		}

		got2, ok := mc.GetNamespaceTargets("ns-b")
		if !ok {
			t.Fatal("expected ok=true for ns-b")
		}
		if len(got2) != 2 {
			t.Errorf("ns-b: expected 2 targets, got %d", len(got2))
		}
	})

	t.Run("empty targets slice is valid", func(t *testing.T) {
		mc := newMiddlewareCache(5 * time.Minute)
		defer mc.Stop()

		mc.SetNamespaceTargets("ns-empty", []gatewayTarget{})

		got, ok := mc.GetNamespaceTargets("ns-empty")
		if !ok {
			t.Fatal("expected ok=true for empty slice")
		}
		if len(got) != 0 {
			t.Errorf("expected 0 targets, got %d", len(got))
		}
	})
}

func TestTTLExpiration(t *testing.T) {
	t.Run("api key namespace expires after TTL", func(t *testing.T) {
		mc := newMiddlewareCache(50 * time.Millisecond)
		defer mc.Stop()

		mc.SetAPIKeyNamespace("key-ttl", "ns-ttl")

		// Should be present immediately
		_, ok := mc.GetAPIKeyNamespace("key-ttl")
		if !ok {
			t.Fatal("expected entry to be present immediately after set")
		}

		// Wait for expiration
		time.Sleep(100 * time.Millisecond)

		_, ok = mc.GetAPIKeyNamespace("key-ttl")
		if ok {
			t.Error("expected entry to be expired after TTL, but it was still present")
		}
	})

	t.Run("namespace targets expire after TTL", func(t *testing.T) {
		mc := newMiddlewareCache(50 * time.Millisecond)
		defer mc.Stop()

		targets := []gatewayTarget{{ip: "10.0.0.1", port: 8080}}
		mc.SetNamespaceTargets("ns-expire", targets)

		// Should be present immediately
		_, ok := mc.GetNamespaceTargets("ns-expire")
		if !ok {
			t.Fatal("expected entry to be present immediately after set")
		}

		// Wait for expiration
		time.Sleep(100 * time.Millisecond)

		_, ok = mc.GetNamespaceTargets("ns-expire")
		if ok {
			t.Error("expected entry to be expired after TTL, but it was still present")
		}
	})

	t.Run("refreshing entry resets TTL", func(t *testing.T) {
		mc := newMiddlewareCache(80 * time.Millisecond)
		defer mc.Stop()

		mc.SetAPIKeyNamespace("key-refresh", "ns-refresh")

		// Wait partway through TTL
		time.Sleep(50 * time.Millisecond)

		// Re-set to refresh TTL
		mc.SetAPIKeyNamespace("key-refresh", "ns-refresh")

		// Wait past the original TTL but not the refreshed one
		time.Sleep(50 * time.Millisecond)

		_, ok := mc.GetAPIKeyNamespace("key-refresh")
		if !ok {
			t.Error("expected entry to still be present after refresh, but it expired")
		}
	})
}
