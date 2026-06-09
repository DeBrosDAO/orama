package ntfy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidator_AcceptsEmpty(t *testing.T) {
	if err := NewValidator().Validate([]byte(`{}`)); err != nil {
		t.Errorf("empty config should be acceptable (all fields optional); got %v", err)
	}
}

func TestValidator_RejectsBadBaseURL(t *testing.T) {
	cases := []string{
		`{"base_url":"ftp://example.com"}`,
		`{"base_url":"example.com"}`,
		`{"base_url":"just-text"}`,
	}
	for _, c := range cases {
		if err := NewValidator().Validate([]byte(c)); err == nil {
			t.Errorf("expected error for %s", c)
		}
	}
}

func TestValidator_AcceptsHttpAndHttps(t *testing.T) {
	// Literal public (documentation-range) IPs so the test is deterministic and
	// never hits real DNS — Validate now does a set-time SSRF resolve for
	// hostname base URLs.
	for _, base := range []string{"http://203.0.113.10:8080", "https://203.0.113.10"} {
		body, _ := json.Marshal(Credentials{BaseURL: base})
		if err := NewValidator().Validate(body); err != nil {
			t.Errorf("base_url=%q rejected: %v", base, err)
		}
	}
}

func TestValidator_RejectsInternalBaseURL(t *testing.T) {
	// SSRF guard: a tenant must not point the push base URL at an internal /
	// reserved address. Literal IPs are rejected without DNS.
	for _, base := range []string{
		"http://169.254.169.254", // cloud metadata
		"http://127.0.0.1:8090",  // loopback (the operator's local ntfy)
		"http://10.0.0.5",        // WireGuard mesh
	} {
		body, _ := json.Marshal(Credentials{BaseURL: base})
		if err := NewValidator().Validate(body); err == nil {
			t.Errorf("internal base_url %q must be rejected (SSRF)", base)
		}
	}
}

func TestValidator_RejectsBadTopicMode(t *testing.T) {
	if err := NewValidator().Validate([]byte(`{"topic_mode":"random"}`)); err == nil {
		t.Error("expected rejection of unknown topic_mode")
	}
}

func TestValidator_AcceptsKnownTopicModes(t *testing.T) {
	for _, mode := range []TopicMode{TopicModeOpaque, TopicModePath, TopicModeUser} {
		body, _ := json.Marshal(Credentials{
			TopicMode:   mode,
			TopicSecret: "non-empty-just-in-case", // satisfies opaque-requires-secret
		})
		if err := NewValidator().Validate(body); err != nil {
			t.Errorf("topic_mode=%q rejected: %v", mode, err)
		}
	}
}

func TestValidator_OpaqueRequiresSecret(t *testing.T) {
	body := []byte(`{"topic_mode":"opaque"}`)
	err := NewValidator().Validate(body)
	if err == nil {
		t.Fatal("expected error: opaque without secret")
	}
	if !strings.Contains(err.Error(), "topic_secret required") {
		t.Errorf("error should mention topic_secret; got %v", err)
	}
}

func TestValidator_RedactNeverEchoesSecrets(t *testing.T) {
	raw := []byte(`{
		"base_url":"https://push.example.com",
		"auth_token":"SECRETAUTH",
		"topic_mode":"opaque",
		"topic_secret":"SECRETHASH"
	}`)
	out, err := NewValidator().Redact(raw)
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	enc, _ := json.Marshal(out)
	if strings.Contains(string(enc), "SECRETAUTH") {
		t.Errorf("redacted leaks auth_token: %s", enc)
	}
	if strings.Contains(string(enc), "SECRETHASH") {
		t.Errorf("redacted leaks topic_secret: %s", enc)
	}
	if !strings.Contains(string(enc), `"has_auth_token":true`) {
		t.Errorf("redacted should signal has_auth_token; got %s", enc)
	}
	if !strings.Contains(string(enc), `"has_topic_secret":true`) {
		t.Errorf("redacted should signal has_topic_secret; got %s", enc)
	}
	if !strings.Contains(string(enc), "push.example.com") {
		t.Errorf("redacted should preserve base_url; got %s", enc)
	}
}

func TestParseCredentials_RoundTrip(t *testing.T) {
	raw, _ := json.Marshal(Credentials{
		BaseURL:     "https://push.example.com",
		AuthToken:   "t-okt",
		TopicMode:   TopicModePath,
		TopicSecret: "",
	})
	c, err := ParseCredentials(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.BaseURL != "https://push.example.com" || c.AuthToken != "t-okt" {
		t.Errorf("round-trip lost fields: %+v", c)
	}
	if c.TopicMode != TopicModePath {
		t.Errorf("topic_mode lost: %q", c.TopicMode)
	}
}
