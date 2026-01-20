package httputil

import (
	"encoding/json"
	"net/http"
)

// WriteJSON writes a JSON response with the given status code.
// It sets the Content-Type header to application/json and encodes the value as JSON.
// Any encoding errors are silently ignored (best-effort).
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes a standardized JSON error response.
// The response format is: {"error": "message"}
func WriteError(w http.ResponseWriter, code int, msg string) {
	WriteJSON(w, code, map[string]any{"error": msg})
}

// WriteSuccess writes a standardized JSON success response.
// The response format is: {"status": "ok"}
func WriteSuccess(w http.ResponseWriter) {
	WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// WriteSuccessWithData writes a success response with additional data fields.
// The response format is: {"status": "ok", ...data}
func WriteSuccessWithData(w http.ResponseWriter, data map[string]any) {
	response := map[string]any{"status": "ok"}
	for k, v := range data {
		response[k] = v
	}
	WriteJSON(w, http.StatusOK, response)
}
