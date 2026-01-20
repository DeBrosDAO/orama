package httputil

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DecodeJSON decodes the request body as JSON into the provided value.
// Returns an error if decoding fails.
func DecodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// DecodeJSONStrict decodes the request body as JSON with strict validation.
// It disallows unknown fields and returns an error if any are present.
func DecodeJSONStrict(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// ReadBody reads the entire request body up to maxBytes.
// Returns the body bytes or an error if reading fails.
func ReadBody(r *http.Request, maxBytes int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, maxBytes))
}

// DecodeBase64 decodes a base64-encoded string to bytes.
func DecodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// EncodeBase64 encodes bytes to a base64-encoded string.
func EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// QueryParam returns the value of a query parameter, or defaultValue if not present.
func QueryParam(r *http.Request, key, defaultValue string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return v
	}
	return defaultValue
}

// QueryParamInt returns the integer value of a query parameter, or defaultValue if not present or invalid.
func QueryParamInt(r *http.Request, key string, defaultValue int) int {
	if v := r.URL.Query().Get(key); v != "" {
		var i int
		if _, err := fmt.Sscanf(v, "%d", &i); err == nil {
			return i
		}
	}
	return defaultValue
}

// QueryParamBool returns the boolean value of a query parameter.
// Returns true if the parameter value is "true", "1", "yes", or "on" (case-insensitive).
// Returns defaultValue if the parameter is not present or has an invalid value.
func QueryParamBool(r *http.Request, key string, defaultValue bool) bool {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultValue
	}
	switch strings.ToLower(v) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return defaultValue
	}
}
