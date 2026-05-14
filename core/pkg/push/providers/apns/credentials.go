// Package apns implements a push.PushProvider backed by Apple Push
// Notification service via token-based (p8 key) authentication.
//
// Feature #72 — direct APNs delivery. The platform owns no Apple
// Developer credentials; each namespace brings its own p8 key, Team
// ID, Key ID, and Bundle ID via PUT /v1/namespace/push-credentials/apns.
// The credential JSON is stored encrypted at rest by pkg/push/credentials
// and parsed here (ParseCredentials) when the namespace dispatcher is
// built.
//
// Reference: https://developer.apple.com/documentation/usernotifications/setting_up_a_remote_notification_server/establishing_a_token-based_connection_to_apns
package apns

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/push/credentials"
)

// Environment selects which APNs endpoint the provider talks to:
//   - "sandbox"    → api.development.push.apple.com (TestFlight / Xcode builds)
//   - "production" → api.push.apple.com           (App Store)
//
// Mismatched environment + device token = "BadDeviceToken" (403) at
// send time. The tenant is responsible for matching their app's build
// channel to the registered environment.
type Environment string

const (
	EnvSandbox    Environment = "sandbox"
	EnvProduction Environment = "production"
)

// Config is the per-namespace APNs credential record. JSON tags mirror
// the public schema tenants PUT to /v1/namespace/push-credentials/apns.
//
// p8_key is the FULL PEM-encoded private key, including the
// `-----BEGIN PRIVATE KEY-----` and `-----END PRIVATE KEY-----` lines.
// Do NOT strip the header/footer — the parsing library requires them.
type Config struct {
	TeamID      string      `json:"team_id"`     // Apple Developer Team ID, 10 chars
	KeyID       string      `json:"key_id"`      // APNs Auth Key ID, 10 chars
	BundleID    string      `json:"bundle_id"`   // e.g. "com.example.app" — must match iOS app
	P8Key       string      `json:"p8_key"`      // PEM-encoded EC P-256 private key
	Environment Environment `json:"environment"` // "sandbox" | "production"
}

// Validator implements credentials.Validator for the APNs provider.
type Validator struct{}

// NewValidator returns the singleton Validator used for registration
// with credentials.Register at gateway startup.
func NewValidator() credentials.Validator { return Validator{} }

// Provider returns "apns".
func (Validator) Provider() string { return "apns" }

// Validate parses + sanity-checks the credential JSON.
//
// We do NOT verify the p8 key against Apple here (would require a
// network round-trip and Apple charges per APNs call). The parse-and-
// shape check catches the obvious bad-input cases at PUT time so
// tenants don't discover a typo only at first-push.
func (Validator) Validate(raw []byte) error {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("apns credentials: invalid JSON: %w", err)
	}
	return validateConfig(c)
}

// Redact returns a JSON-safe view that NEVER echoes the p8 key. Other
// fields (Team ID, Key ID, Bundle ID, Environment) are not secret in
// the cryptographic sense — they're identifiers Apple prints in your
// dashboard — so we return them verbatim, which lets the tenant
// confirm what's configured without needing to PUT-and-fetch again.
func (Validator) Redact(raw []byte) (interface{}, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("apns redact: invalid JSON: %w", err)
	}
	return struct {
		TeamID      string      `json:"team_id"`
		KeyID       string      `json:"key_id"`
		BundleID    string      `json:"bundle_id"`
		Environment Environment `json:"environment"`
		HasP8Key    bool        `json:"has_p8_key"`
	}{
		TeamID:      c.TeamID,
		KeyID:       c.KeyID,
		BundleID:    c.BundleID,
		Environment: c.Environment,
		HasP8Key:    c.P8Key != "",
	}, nil
}

// ParseCredentials decodes the raw JSON stored in
// namespace_push_credentials.credentials_json into a typed Config.
// Called by the gateway dependency factory when building a per-
// namespace dispatcher.
func ParseCredentials(raw []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("apns ParseCredentials: %w", err)
	}
	if err := validateConfig(c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// validateConfig is the shared validator used by both Validate and
// ParseCredentials. Returns nil iff the Config is acceptable.
func validateConfig(c Config) error {
	if c.TeamID == "" {
		return fmt.Errorf("apns credentials: team_id required")
	}
	if len(c.TeamID) != 10 {
		return fmt.Errorf("apns credentials: team_id must be 10 characters (got %d)", len(c.TeamID))
	}
	if c.KeyID == "" {
		return fmt.Errorf("apns credentials: key_id required")
	}
	if len(c.KeyID) != 10 {
		return fmt.Errorf("apns credentials: key_id must be 10 characters (got %d)", len(c.KeyID))
	}
	if c.BundleID == "" {
		return fmt.Errorf("apns credentials: bundle_id required")
	}
	if !strings.Contains(c.BundleID, ".") {
		return fmt.Errorf("apns credentials: bundle_id must be reverse-DNS (e.g. com.example.app), got %q", c.BundleID)
	}
	if c.P8Key == "" {
		return fmt.Errorf("apns credentials: p8_key required")
	}
	if !strings.Contains(c.P8Key, "BEGIN PRIVATE KEY") {
		return fmt.Errorf("apns credentials: p8_key must be PEM-encoded (missing BEGIN PRIVATE KEY header)")
	}
	switch c.Environment {
	case EnvSandbox, EnvProduction:
		// ok
	case "":
		return fmt.Errorf("apns credentials: environment required (\"sandbox\" or \"production\")")
	default:
		return fmt.Errorf("apns credentials: environment must be \"sandbox\" or \"production\" (got %q)", c.Environment)
	}
	return nil
}
