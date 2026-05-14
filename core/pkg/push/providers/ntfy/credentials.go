package ntfy

// credentials.go — ntfy's plug-in for pkg/push/credentials (feature #72).
//
// Lets tenants store their ntfy auth_token (and optionally override the
// base_url for full server sovereignty) via PUT
// /v1/namespace/push-credentials/ntfy.
//
// Topic-format selection is also configured here. The opaque sha256
// mode is the default (privacy-first); tenants can opt into readable
// modes when they actively want them.
//
// Backward-compat: tenants whose ntfy_auth_token is still in
// namespace_push_config (migration 026) continue to work — the gateway
// factory in dependencies.go reads from BOTH sources during the
// migration window, with the new credentials store taking precedence.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/push/credentials"
)

// TopicMode selects how device tokens (ntfy topics) are generated.
// Tenants pick at namespace registration time; their iOS/Android
// clients must agree on the same mode or messages get routed to the
// wrong topic and never delivered.
type TopicMode string

const (
	// TopicModeOpaque hashes (namespace | userId | topic_secret) to
	// sha256 and uses the hex digest as the topic. Leaks NOTHING to a
	// public-topic scraper. Recommended default for privacy.
	TopicModeOpaque TopicMode = "opaque"

	// TopicModePath uses "ns/<namespace>/<userId>" as the topic.
	// Readable / debuggable; exposes which users have push enabled to
	// anyone enumerating topics.
	TopicModePath TopicMode = "path"

	// TopicModeUser uses just "<userId>" as the topic. Minimal — leaks
	// user IDs but not namespace.
	TopicModeUser TopicMode = "user"
)

// Credentials is the per-namespace ntfy credential record. JSON tags
// mirror the public schema tenants PUT to
// /v1/namespace/push-credentials/ntfy.
//
// Distinct from the existing `Config` (which is the construction-time
// HTTP-client config); the gateway factory parses Credentials, then
// merges them into a Config used to instantiate the Provider.
//
// All fields are optional — an empty record is valid and means "use
// the gateway YAML defaults". The gateway factory layers this on top
// of any legacy 026 row (which takes effect only if the new record is
// absent).
//
// `topic_secret` is required when `topic_mode = "opaque"`. The same
// secret must be known to both the device client (to compute its own
// topic) and the gateway (to compute the topic it sends to). Tenants
// MUST distribute the secret to their clients via a path they trust
// (typically baked into the app's signed config).
type Credentials struct {
	BaseURL     string    `json:"base_url,omitempty"`
	AuthToken   string    `json:"auth_token,omitempty"`
	TopicMode   TopicMode `json:"topic_mode,omitempty"`
	TopicSecret string    `json:"topic_secret,omitempty"`
}

// Validator implements credentials.Validator for the ntfy provider.
type Validator struct{}

// NewValidator returns the singleton Validator for registration with
// credentials.Register at gateway startup.
func NewValidator() credentials.Validator { return Validator{} }

// Provider returns "ntfy".
func (Validator) Provider() string { return "ntfy" }

// Validate parses + checks the credential JSON. Soft on missing fields
// (each is independently optional), strict on schema correctness.
func (Validator) Validate(raw []byte) error {
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("ntfy credentials: invalid JSON: %w", err)
	}
	return validateCredentials(c)
}

// Redact returns a JSON-safe view that never echoes the auth token or
// topic secret. Non-secret fields (BaseURL, TopicMode) are returned
// verbatim so tenants can confirm what's configured.
func (Validator) Redact(raw []byte) (interface{}, error) {
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("ntfy redact: invalid JSON: %w", err)
	}
	return struct {
		BaseURL        string    `json:"base_url,omitempty"`
		TopicMode      TopicMode `json:"topic_mode,omitempty"`
		HasAuthToken   bool      `json:"has_auth_token"`
		HasTopicSecret bool      `json:"has_topic_secret"`
	}{
		BaseURL:        c.BaseURL,
		TopicMode:      c.TopicMode,
		HasAuthToken:   c.AuthToken != "",
		HasTopicSecret: c.TopicSecret != "",
	}, nil
}

// ParseCredentials decodes raw JSON from namespace_push_credentials
// into a typed Credentials. Returns an error if validation fails.
func ParseCredentials(raw []byte) (Credentials, error) {
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return Credentials{}, fmt.Errorf("ntfy ParseCredentials: %w", err)
	}
	if err := validateCredentials(c); err != nil {
		return Credentials{}, err
	}
	return c, nil
}

// validateCredentials is the shared validator used by both Validate and
// ParseCredentials.
func validateCredentials(c Credentials) error {
	if c.BaseURL != "" {
		if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
			return fmt.Errorf("ntfy credentials: base_url must start with http:// or https:// (got %q)", c.BaseURL)
		}
	}
	if c.TopicMode != "" {
		switch c.TopicMode {
		case TopicModeOpaque, TopicModePath, TopicModeUser:
			// ok
		default:
			return fmt.Errorf("ntfy credentials: topic_mode must be one of \"opaque\", \"path\", \"user\" (got %q)", c.TopicMode)
		}
	}
	if c.TopicMode == TopicModeOpaque && c.TopicSecret == "" {
		return fmt.Errorf("ntfy credentials: topic_secret required when topic_mode=\"opaque\"")
	}
	// AuthToken is always optional — public ntfy servers don't require
	// auth. No length check; ntfy accepts arbitrary bearer tokens.
	return nil
}
