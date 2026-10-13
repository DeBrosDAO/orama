package push

import (
	"errors"
	"fmt"
)

// validProviders is the allowlist for the `provider` field on RegisterDevice.
// Keep in sync with what the dispatcher actually has registered at startup.
//
// "apns_voip" (bugboard #408) is the PushKit/CallKit variant of "apns" —
// same underlying credentials, distinct dispatcher entry. Tenants
// register a second PushDevice row per iPhone with the PushKit
// voipPushToken to enable CallKit-triggering incoming-call pushes,
// keyed by a distinct device_id (typically `<base>:voip`) so the
// `device_id` PK doesn't collide with the alert-path row.
var validProviders = map[string]struct{}{
	"ntfy":      {},
	"expo":      {},
	"apns":      {},
	"apns_voip": {},
}

// MaxTokenBytes caps the device-token length to prevent abuse.
// Real ntfy topic paths and Expo tokens are well under this.
const MaxTokenBytes = 512

const (
	// maxRegisterBodyBytes caps a device or topic registration body: a few
	// short fields and a token of at most MaxTokenBytes.
	maxRegisterBodyBytes = 4096

	// maxSendBodyBytes caps a send body; generous for Data payloads.
	maxSendBodyBytes = 64 * 1024
)

// validateProviderToken checks the provider and token of a device or topic
// registration. Both are already trimmed.
func validateProviderToken(provider, token string) error {
	if _, ok := validProviders[provider]; !ok {
		return fmt.Errorf("unknown provider: %s", provider)
	}
	if token == "" {
		return errors.New("token required")
	}
	if len(token) > MaxTokenBytes {
		return errors.New("token too long")
	}
	return nil
}
