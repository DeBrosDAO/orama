// Package credentials provides per-namespace, per-provider push-credential
// storage with at-rest encryption.
//
// This package is intentionally provider-agnostic: it knows how to put
// and get an opaque JSON blob keyed by (namespace, provider), and it
// delegates schema validation + redaction to the provider package via a
// Validator registry. Adding a new push provider — APNs, FCM, SMS,
// whatever — requires only:
//
//	1. A provider package that implements credentials.Validator.
//	2. A call to credentials.Register(<provider-name>, validator) from
//	   the gateway dependency wiring.
//
// No changes here; no schema migration; no new HTTP endpoint.
//
// Feature #72. Mirrors the per-namespace LRU+TTL caching pattern from
// pkg/ratelimit (#69) so cross-gateway config staleness is bounded
// without a pub/sub broadcast layer.
package credentials

import (
	"context"
	"errors"
	"time"
)

// Credential is one row of namespace_push_credentials.
//
// JSON is plaintext in this struct — encryption happens at the storage
// boundary. Callers who load Credentials from the store must treat JSON
// as sensitive material (never log it, never echo it back unredacted).
type Credential struct {
	Namespace string
	Provider  string
	JSON      []byte // provider-specific schema; owned by the provider package
	UpdatedAt int64  // unix seconds
	UpdatedBy string // free-form audit (wallet address, operator ID, etc.)
}

// Store reads and writes per-(namespace, provider) credentials. Production
// implementation is rqlite-backed (see store.go); tests typically swap
// in an in-memory map.
type Store interface {
	// Get returns the credential, or ErrNotFound if no row exists for
	// (namespace, provider).
	Get(ctx context.Context, namespace, provider string) (*Credential, error)

	// Upsert inserts or replaces the credential. cred.UpdatedAt and
	// cred.UpdatedBy must be populated by the caller.
	Upsert(ctx context.Context, cred Credential) error

	// Delete removes the credential. Idempotent — no error if the row
	// didn't exist.
	Delete(ctx context.Context, namespace, provider string) error

	// ListProviders returns the provider names that have a row for the
	// given namespace. Used by the "what's configured" summary endpoint.
	// Order is unspecified.
	ListProviders(ctx context.Context, namespace string) ([]string, error)
}

// Validator is implemented by each provider package to validate and
// redact its own credential JSON schema. The credentials package itself
// never inspects the JSON.
//
// Validate is called by the PUT handler before storage; it should return
// a descriptive error for any malformed or out-of-spec value so the
// tenant gets actionable feedback at PUT time (not at first-push time).
//
// Redact is called by the GET handler after decryption; it MUST NOT
// echo secret material back to the caller. Standard pattern: replace
// each secret string with a boolean "has_<field>" flag, leave non-secret
// fields as-is, and return any JSON-marshalable struct.
type Validator interface {
	// Provider returns the provider name (e.g. "apns", "ntfy", "fcm"). Must
	// match the URL path segment used at registration.
	Provider() string

	// Validate parses rawJSON and returns nil if the schema is acceptable
	// for this provider. Errors should be human-readable; they're surfaced
	// directly to the tenant in the 400 response.
	Validate(rawJSON []byte) error

	// Redact returns a JSON-serializable view of rawJSON with all secret
	// fields replaced by `has_<field>` booleans (or otherwise made safe
	// for return over HTTP).
	Redact(rawJSON []byte) (interface{}, error)
}

// Sentinel errors.
var (
	// ErrNotFound is returned by Store.Get when no credential exists for
	// (namespace, provider). Callers fall back to the legacy 026 config
	// (during the ntfy/expo migration window) or treat as "not configured".
	ErrNotFound = errors.New("credentials: not found")

	// ErrUnknownProvider is returned by handlers when the URL provider
	// segment doesn't have a registered Validator. New providers must
	// register their Validator at gateway startup (see registry.go).
	ErrUnknownProvider = errors.New("credentials: unknown provider")

	// ErrInvalidNamespace / ErrInvalidProvider catch programmer / input
	// errors at the storage boundary.
	ErrInvalidNamespace = errors.New("credentials: namespace required")
	ErrInvalidProvider  = errors.New("credentials: provider required")
)

// cacheEntryTTL bounds how long a stale Manager cache entry can serve
// before the next lookup re-reads the store. Mirrors the ratelimit
// Manager's TTL (30s) — short enough that operator config changes
// propagate across multi-gateway deployments quickly, long enough that
// the store isn't hit on every push.
const cacheEntryTTL = 30 * time.Second

// defaultCacheCap caps the Manager's LRU. Each entry is a small (~1 KB)
// decoded credential; 1024 is generous and bounds memory under abuse.
const defaultCacheCap = 1024
