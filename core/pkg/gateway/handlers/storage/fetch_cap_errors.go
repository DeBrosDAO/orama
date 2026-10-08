package storage

import (
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// The typed refusals of the fetch-capability routes (bugboard #266). Every one
// carries {error, code, hint} like the rest of the credential refusals
// (docs/AUTH.md "Error codes"); the code is the contract.
const (
	// CodeFetchCapMissing — a relayed download with no X-Orama-Fetch-Cap header.
	CodeFetchCapMissing = "FETCH_CAP_MISSING"
	// CodeFetchCapInvalid — the capability is forged, expired, for another CID
	// or namespace, or not a fetch capability at all. One code for all of them:
	// which it was is of use only to somebody probing.
	CodeFetchCapInvalid = "FETCH_CAP_INVALID"
	// CodeFetchCapRevoked — the capability was valid and has been revoked, itself
	// or through the device that issued it.
	CodeFetchCapRevoked = "FETCH_CAP_REVOKED"
	// CodeFetchCapNotAlone — the capability arrived beside a credential, which
	// would tie the fetch to an account.
	CodeFetchCapNotAlone = "FETCH_CAP_NOT_ALONE"
	// CodeFetchCapDeviceRequired — a mint from a session bound to no device.
	CodeFetchCapDeviceRequired = "FETCH_CAP_DEVICE_REQUIRED"
	// CodeFetchCapRevokeKeyInvalid — a revoke by id without the revoke key the
	// mint returned for that id, or with another one.
	CodeFetchCapRevokeKeyInvalid = "FETCH_CAP_REVOKE_KEY_INVALID"
	// CodeFetchCapUnavailable — the gateway could not check the capability right
	// now (no cluster secret, or the revocation list cannot be read). Retryable.
	CodeFetchCapUnavailable = "FETCH_CAP_UNAVAILABLE"
)

// fetchCapHints say what to do about each refusal.
var fetchCapHints = map[string]string{
	CodeFetchCapMissing:          "send the capability in the X-Orama-Fetch-Cap header",
	CodeFetchCapInvalid:          "ask the owner for a new capability for this CID",
	CodeFetchCapRevoked:          "this capability was revoked; ask the owner for another",
	CodeFetchCapNotAlone:         "send the capability with no Authorization header, API key or session",
	CodeFetchCapDeviceRequired:   "mint from a session bound to a device; an API key or a workload token has none",
	CodeFetchCapRevokeKeyInvalid: "send the revoke_key the mint returned for this id in the X-Orama-Revoke-Key header",
	CodeFetchCapUnavailable:      "this is temporary; retry in a few seconds with the same capability",
}

// writeFetchCapError writes {error, code, hint}.
func writeFetchCapError(w http.ResponseWriter, status int, code, message string) {
	body := map[string]any{"error": message, "code": code}
	if hint := fetchCapHints[code]; hint != "" {
		body["hint"] = hint
	}
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gateway", charset="UTF-8"`)
	}
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", fetchCapRetryAfterSeconds)
	}
	httputil.WriteJSON(w, status, body)
}

// fetchCapRetryAfterSeconds is the Retry-After of a FETCH_CAP_UNAVAILABLE: a
// little over the revocation list's retry interval.
const fetchCapRetryAfterSeconds = "2"
