package gateway

import (
	"net/http"
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/httputil"
)

// The typed refusals of the anonymous relay (bugboard #266). The relay takes no
// credential, so these are not credential refusals and are not AuthCode values;
// they carry {error, code, hint} like those do (docs/whitepaper/technical-reference/vol1/14-authorization.md "Error codes").
const (
	// CodeRelayDestinationNotAllowed — the relay reaches only hosts under its
	// allowed suffixes, on port 443, and never an IP literal.
	CodeRelayDestinationNotAllowed = "RELAY_DESTINATION_NOT_ALLOWED"
	// CodeRelayUnavailable — the relay could not carry the stream: the
	// anonymity network is down on this node, or the destination was not reached
	// through it. It never falls back to a direct connection.
	CodeRelayUnavailable = "RELAY_UNAVAILABLE"
)

// relayRetryAfterSeconds is what a refused client is told to wait.
const relayRetryAfterSeconds = 60

var relayHints = map[string]string{
	CodeRelayDestinationNotAllowed:      "ask for a host under the relay's allowed suffixes (this cluster's namespace hosts by default), on port 443",
	CodeRelayUnavailable:                "this is temporary; retry, or pick another relay",
	string(httputil.ErrCodeRateLimited): "wait for Retry-After, then retry, or pick another relay",
}

// writeRelayError writes {error, code, hint}.
func writeRelayError(w http.ResponseWriter, status int, code, message string) {
	body := map[string]any{"error": message, "code": code}
	if hint := relayHints[code]; hint != "" {
		body["hint"] = hint
	}
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.Itoa(relayRetryAfterSeconds))
	}
	writeJSON(w, status, body)
}

// writeRelayRateLimited is the 429 of the relay.
func writeRelayRateLimited(w http.ResponseWriter, message string) {
	writeRelayError(w, http.StatusTooManyRequests, string(httputil.ErrCodeRateLimited), message)
}
