package rqlite

import (
	"encoding/json"
	"errors"
	"net/http"
)

// MaxRequestBodyBytes bounds a request body on the ORM routes (query, exec,
// find, find-one, select, transaction, create-table, drop-table). SQL text and
// its arguments travel in that body, so this is the largest statement a caller
// can send; a body over it is refused with 413 before any of it is parsed into
// a statement or reaches the database.
const MaxRequestBodyBytes = 4 << 20

// decodeBody reads the request's JSON body into v. It reports false after
// answering the request: 413 when the body is over MaxRequestBodyBytes,
// otherwise 400 with invalid.
func decodeBody(w http.ResponseWriter, r *http.Request, v any, invalid string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes)
	err := json.NewDecoder(r.Body).Decode(v)
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds the 4 MiB limit")
		return false
	}
	writeError(w, http.StatusBadRequest, invalid)
	return false
}
