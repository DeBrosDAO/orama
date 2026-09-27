package auth

import "encoding/json"

// LoginDevice is a device key the sign-in binds. PublicJWK is the public
// half only. Sign signs the gateway's sign-in message with the private half,
// which never leaves this process.
type LoginDevice struct {
	ID        string
	PublicJWK json.RawMessage
	Sign      func(message string) (string, error)
}
