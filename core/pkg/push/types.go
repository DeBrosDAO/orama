// Package push provides a generic push-notification abstraction for Orama.
//
// Apps register devices with a provider name ("ntfy", "expo", "apns", ...)
// and a provider-specific token. The PushDispatcher routes outbound push
// messages to the matching provider so call sites stay backend-agnostic.
//
// Long-term the platform aims to drop Expo in favour of direct APNs +
// ntfy. The abstraction makes that swap a configuration change rather
// than a code change.
package push

import (
	"context"
	"errors"
)

// PushPriority signals delivery urgency to the provider.
// Providers that don't support priorities ignore the value.
type PushPriority string

const (
	PriorityNormal PushPriority = "normal"
	PriorityHigh   PushPriority = "high"
)

// PushMessage is the provider-agnostic message format.
//
// DeviceToken is the provider-specific identifier (e.g. an ntfy topic,
// an Expo push token, an APNs device token). The PushDispatcher fills
// it in per-device before calling Send.
//
// TargetProvider is consumed by the DISPATCHER (not by providers) to
// filter the device list pre-send. Empty = fan out to all registered
// devices regardless of provider (back-compat default). Non-empty =
// dispatcher skips any device whose Provider field doesn't equal this
// value. Bugboard #408 — needed so a chat-alert message-push-handler
// can target "apns" only and avoid waking the user's "apns_voip"
// (PushKit/CallKit) device on every text. Providers themselves ignore
// this field.
type PushMessage struct {
	DeviceToken    string
	Title          string
	Body           string
	Data           map[string]interface{}
	Badge          int
	Sound          string
	Channel        string // "messages", "calls", etc — provider may map to its own channel concept
	Priority       PushPriority
	TargetProvider string // dispatcher-side filter; "" = fanout. See type doc.
}

// PushProvider is implemented by each backend (ntfy, expo, apns).
type PushProvider interface {
	Name() string
	// Send delivers a single push. Returning an error counts as a delivery
	// failure for that device; the dispatcher logs it and continues.
	Send(ctx context.Context, msg PushMessage) error
}

// PushDevice represents a registered push target for a user.
//
// Token is plaintext in this struct — encryption happens at the storage
// layer. Callers who load Devices from the store must treat tokens as
// sensitive material (don't log them).
type PushDevice struct {
	ID        string
	Namespace string
	UserID    string
	DeviceID  string // app-provided
	Provider  string // matches PushProvider.Name()
	Token     string
	Platform  string // "ios" | "android" | "web"
	AppVer    string
	CreatedAt int64 // unix seconds
	UpdatedAt int64
	LastSeen  int64
}

// PushDeviceStore persists per-user device registrations.
type PushDeviceStore interface {
	// Upsert registers or updates a device. The Token is encrypted by the
	// implementation before being written to durable storage.
	Upsert(ctx context.Context, dev PushDevice) error

	// Delete removes a single device by ID, scoped to the namespace.
	Delete(ctx context.Context, namespace, id string) error

	// ListForUser returns all devices for a user within a namespace.
	// Tokens in the returned slice are decrypted.
	ListForUser(ctx context.Context, namespace, userID string) ([]PushDevice, error)
}

// Sentinel errors.
var (
	// ErrUnknownProvider is returned by the dispatcher when a device
	// references a provider that isn't registered.
	ErrUnknownProvider = errors.New("push: unknown provider")
	// ErrEmptyToken is returned by providers when called with an empty
	// DeviceToken.
	ErrEmptyToken = errors.New("push: empty device token")
	// ErrEmptyContent is returned by providers when the message has no
	// title, body, badge, sound, or content-available marker. Apple
	// silently accepts (HTTP 200) and drops such pushes — caught upfront
	// so the failure surfaces instead of looking like success. Bugboard
	// #348 root-cause class.
	ErrEmptyContent = errors.New("push: empty visible-content payload (set title/body, badge, sound, or content_available)")
)

// PushError is the structured error type returned by providers when the
// remote service (APNs, ntfy, etc.) responds with a failure. Carries the
// HTTP status + provider-specific reason code so the caller can decide
// how to react (e.g. delete stale tokens on 410, retry on 5xx).
//
// Used via errors.As at the dispatcher layer to build a per-device
// result for the WASM-callable `oh.PushSendV2` host function.
type PushError struct {
	// HTTPStatus is the HTTP/2 :status from the remote (e.g. 400, 410,
	// 500). 0 means the failure happened before the HTTP exchange
	// (network, validation, etc.) — see Message for details.
	HTTPStatus int
	// Reason is the provider-specific machine-readable reason string
	// (e.g. APNs `BadDeviceToken`, `Unregistered`). Empty for non-HTTP
	// failures.
	Reason string
	// Message is the human-readable summary, suitable for logs.
	Message string
	// Unregistered is a shortcut for "the remote says this token is
	// dead — delete the device row". Maps to APNs HTTP 410 with reason
	// `Unregistered`. Other providers set this when they have an
	// equivalent signal.
	Unregistered bool
	// Wrapped is the underlying error if this PushError wraps another
	// error type. Allows errors.Is / errors.As traversal.
	Wrapped error
}

// Error implements the error interface.
func (e *PushError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Unwrap allows errors.Is / errors.As to traverse.
func (e *PushError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Wrapped
}

// DeviceSendResult is the per-device outcome of a SendToUserDetailed
// call. Used by the rich-result push host fn so WASM callers can see
// exactly what happened per device — and react (e.g. delete the device
// row on Unregistered, retry on 5xx, log unknowns).
type DeviceSendResult struct {
	DeviceID     string `json:"device_id"`
	Provider     string `json:"provider"`
	Success      bool   `json:"success"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Message      string `json:"message,omitempty"`
	Unregistered bool   `json:"unregistered,omitempty"`

	// err carries the underlying error (preserves the full chain for
	// errors.Is / errors.As). Unexported so json.Marshal ignores it —
	// only the structured fields above appear in the WASM-visible
	// envelope. Used by the legacy SendToUser to preserve the sentinel
	// errors.Is contract for callers built before SendToUserDetailed.
	err error `json:"-"`
}

// Err returns the underlying error for this device's send attempt, or
// nil if it succeeded. Exposed as a method so external callers can
// still use errors.Is/As against per-device failures.
func (r DeviceSendResult) Err() error { return r.err }

// SendDetailedResult is the aggregate return from SendToUserDetailed.
// One DeviceSendResult per device the user has registered in the
// namespace. Ok is true when EVERY device succeeded.
type SendDetailedResult struct {
	Ok               bool               `json:"ok"`
	DevicesAttempted int                `json:"devices_attempted"`
	DevicesSucceeded int                `json:"devices_succeeded"`
	Results          []DeviceSendResult `json:"results"`
}
