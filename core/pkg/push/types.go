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
type PushMessage struct {
	DeviceToken string
	Title       string
	Body        string
	Data        map[string]interface{}
	Badge       int
	Sound       string
	Channel     string // "messages", "calls", etc — provider may map to its own channel concept
	Priority    PushPriority
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
)
