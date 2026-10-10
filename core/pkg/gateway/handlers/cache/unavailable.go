package cache

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	olriclib "github.com/olric-data/olric"
)

// cacheUnavailableMessage is what a caller is told when the cache cannot be
// reached or does not answer in time: it is the cache being down, not the
// request being wrong, and a retry is the right response.
const cacheUnavailableMessage = "cache unavailable; retry"

// retryAfterSeconds is the Retry-After on a 503 from an unreachable cache.
const retryAfterSeconds = "5"

// transportFailureText are the fragments of a transport failure's message.
//
// The text is matched because the Olric client keeps nothing else: it reads
// the error with protocol.ConvertError, which rebuilds any error that is not
// one of its registered codes from the message alone, so a refused dial, a
// read that timed out and a reset connection reach the handler as plain
// errors with no type to test (olric v0.7.4 internal/protocol/errors.go:81).
var transportFailureText = []string{
	"timeout",
	"deadline exceeded",
	"connection refused",
	"connection reset",
	"broken pipe",
	"unreachable",
	"dial tcp",
	"eof",
}

// isCacheUnreachable reports whether err is the transport failing rather than
// Olric answering: the connection was refused or reset, the member did not
// answer within the client's I/O deadline (olric.OperationTimeout), or the
// request's own deadline passed while waiting.
func isCacheUnreachable(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, olriclib.ErrConnRefused) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, fragment := range transportFailureText {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}

// writeUnavailable answers 503 with a Retry-After.
func writeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", retryAfterSeconds)
	writeError(w, http.StatusServiceUnavailable, cacheUnavailableMessage)
}
