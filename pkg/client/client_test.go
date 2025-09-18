package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
)

// MakeJWT creates a minimal JWT-like token with a json payload
// diriving from the namespace.
func makeJWT(ns string) string {
	payload := map[string]string{"Namespace": ns}
	b, _ := json.Marshal(payload)
	return "header." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

func TestParseJWTNamespace(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		token := makeJWT("myns")
		ns, err := parseJWTNamespace(token)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ns != "myns" {
			t.Fatalf("expected namespace 'myns', got %q", ns)
		}
	})

	t.Run("invalid_format", func(t *testing.T) {
		_, err := parseJWTNamespace("invalidtoken")

		if err == nil {
			t.Fatalf("expected error for invalid format")
		}
	})

	t.Run("invalid_base64", func(t *testing.T) {
		// second part not valid base64url
		_, err := parseJWTNamespace("h.invalid!!payload.sig")
		if err == nil {
			t.Fatalf("expected error for invalid base64 payload")
		}
	})
}

func TestParseAPIKeyNamespace(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		ns, err := parseAPIKeyNamespace("ak_random:apins")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ns != "apins" {
			t.Fatalf("expected 'apins', got %q", ns)
		}
	})

	t.Run("invalid_format", func(t *testing.T) {
		_, err := parseAPIKeyNamespace("no-colon")
		if err == nil {
			t.Fatalf("expected error for invalid format")
		}
	})

	t.Run("empty_key", func(t *testing.T) {
		_, err := parseAPIKeyNamespace("   ")
		if err == nil {
			t.Fatalf("expected error for empty key")
		}
	})
}

func TestDeriveNamespace(t *testing.T) {
	t.Run("prefers_jwt_over_apikey_and_appname", func(t *testing.T) {
		cfg := &ClientConfig{
			AppName: "appname",
			JWT:     makeJWT("jwtns"),
			APIKey:  "ak_x:apikns",
		}
		c := &Client{config: cfg}
		ns, err := c.deriveNamespace()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ns != "jwtns" {
			t.Fatalf("expected jwtns, got %q", ns)
		}
	})

	t.Run("uses_apikey_when_no_jwt", func(t *testing.T) {
		cfg := &ClientConfig{
			AppName: "appname",
			JWT:     "",
			APIKey:  "ak_x:apikns",
		}
		c := &Client{config: cfg}
		ns, err := c.deriveNamespace()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ns != "apikns" {
			t.Fatalf("expected apikns, got %q", ns)
		}
	})

	t.Run("fallsback_to_appname", func(t *testing.T) {
		cfg := &ClientConfig{
			AppName: "appname",
			JWT:     "",
			APIKey:  "",
		}
		c := &Client{config: cfg}
		ns, err := c.deriveNamespace()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ns != "appname" {
			t.Fatalf("expected appname, got %q", ns)
		}
	})
}

func TestRequireAccess(t *testing.T) {
	t.Run("internal_context_bypasses_auth", func(t *testing.T) {
		c := &Client{config: nil} // no config
		ctx := WithInternalAuth(context.Background())
		if err := c.requireAccess(ctx); err != nil {
			t.Fatalf("expected nil error for internal context, got %v", err)
		}
	})

	t.Run("missing_credentials_denied", func(t *testing.T) {
		cfg := &ClientConfig{AppName: "app"}
		c := &Client{config: cfg}
		if err := c.requireAccess(context.Background()); err == nil {
			t.Fatalf("expected error when credentials missing")
		}
	})

	t.Run("namespace_override_mismatch_denied", func(t *testing.T) {
		cfg := &ClientConfig{AppName: "app", APIKey: "ak_x:app"}
		c := &Client{config: cfg}
		// set resolved namespace to "app" to simulate derived namespace
		c.resolvedNamespace = "app"

		// override pubsub namespace to something else
		ctx := pubsub.WithNamespace(context.Background(), "other")
		if err := c.requireAccess(ctx); err == nil {
			t.Fatalf("expected namespace mismatch error for pubsub override")
		}

		// override pubsub namespace to something else
		ctx2 := pubsub.WithNamespace(context.Background(), "other")
		if err := c.requireAccess(ctx2); err == nil {
			t.Fatalf("expected namespace mismatch error for pubsub override")
		}
	})

	t.Run("matching_namespace_override_allowed", func(t *testing.T) {
		cfg := &ClientConfig{AppName: "app", APIKey: "ak_x:app"}
		c := &Client{config: cfg}
		c.resolvedNamespace = "app"

		ctx := WithNamespace(context.Background(), "app") // sets both storage & pubsub overrides to "app"
		if err := c.requireAccess(ctx); err != nil {
			t.Fatalf("expected no error for matching namespace override, got %v", err)
		}
	})
}

func TestHealth(t *testing.T) {
	cfg := &ClientConfig{AppName: "app"}
	c := &Client{config: cfg}

	// default disconnected
	h, err := c.Health()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Status != "unhealthy" {
		t.Fatalf("expected unhealthy when not connected, got %q", h.Status)
	}

	// mark connected
	c.connected = true
	h2, _ := c.Health()
	if h2.Status != "healthy" {
		t.Fatalf("expected healthy when connected, got %q", h2.Status)
	}
}
