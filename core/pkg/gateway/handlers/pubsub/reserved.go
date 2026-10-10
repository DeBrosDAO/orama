package pubsub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

const (
	// reservedEnvelopeKey is the top-level JSON key the platform stamps on its
	// own events (ephemeral state, see serverless.EphemeralEvent). A client that
	// reads a message with this key trusts it came from the platform, so no
	// caller of a publish route may be able to send one.
	reservedEnvelopeKey = "_orama"

	// CodeReservedEnvelope is the wire code of a publish refused because its
	// payload is a JSON object carrying the reserved `_orama` key.
	CodeReservedEnvelope = "PUBSUB_RESERVED_KEY"

	// CodeUnreadableObject is the wire code of a publish refused because its
	// payload starts as a JSON object but does not parse strictly, so whether it
	// carries the reserved key cannot be told.
	CodeUnreadableObject = "PUBSUB_INVALID_OBJECT"
)

// utf8BOM is a byte-order mark. Browsers' TextDecoder and iOS's
// JSONSerialization strip it before parsing, so a payload carrying one is still
// an object to them.
var utf8BOM = []byte("\xEF\xBB\xBF")

// envelopeVerdict is what a payload is, as far as the reserved key goes.
type envelopeVerdict int

const (
	envelopeClear      envelopeVerdict = iota // not an object, or an object without the key
	envelopeReserved                          // an object carrying the reserved key
	envelopeUnreadable                        // looks like an object, is not strict JSON
)

// classifyEnvelope decides whether data may be published. Keys are compared
// case-insensitively because Go's encoding/json, which a subscriber may well
// decode with, matches struct tags that way. Anything that is not a JSON object
// is not an envelope: the discriminator only exists on objects.
//
// It fails closed. A payload that starts as an object but does not parse
// strictly here (a byte-order mark, nesting past Go's limit, trailing commas)
// may still be an object to a more lenient subscriber, with the reserved key in
// it, so it is refused rather than published unchecked.
func classifyEnvelope(data []byte) envelopeVerdict {
	trimmed := bytes.TrimLeft(bytes.TrimPrefix(bytes.TrimLeft(data, " \t\r\n"), utf8BOM), " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return envelopeClear
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return envelopeUnreadable
	}
	for key := range fields {
		if strings.EqualFold(key, reservedEnvelopeKey) {
			return envelopeReserved
		}
	}
	return envelopeClear
}

// refuseReservedEnvelope answers a publish whose payload carries the reserved
// key. It reports whether the publish was refused; on true the response has
// been written. what names the message ("message 3") for the client.
func refuseReservedEnvelope(w http.ResponseWriter, data []byte, what string) bool {
	var msg, code string
	switch classifyEnvelope(data) {
	case envelopeReserved:
		msg = what + " carries the reserved top-level key \"" + reservedEnvelopeKey + "\": " +
			"it marks events the platform itself publishes, so it cannot be sent through a publish route; " +
			"rename the key"
		code = CodeReservedEnvelope
	case envelopeUnreadable:
		msg = what + " starts as a JSON object but is not valid JSON (a byte-order mark, trailing commas " +
			"or nesting too deep); send strict JSON, or a payload that is not an object"
		code = CodeUnreadableObject
	default:
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
	return true
}
