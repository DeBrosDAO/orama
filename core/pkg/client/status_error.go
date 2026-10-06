package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	orerrors "github.com/DeBrosOfficial/network/pkg/errors"
)

// gatewayStatusError is a gateway answering with an error status. It is one of
// the typed errors docs/GO_CLIENT_SDK.md promises: errors.IsUnauthorized and
// the other helpers recognise it by its status. Storage used to return the
// status as text, which none of them could read.
type gatewayStatusError struct {
	op      string
	status  int
	message string
}

func (e *gatewayStatusError) Error() string {
	if e.op == "" {
		return fmt.Sprintf("the gateway answered %d: %s", e.status, e.message)
	}
	return fmt.Sprintf("%s: the gateway answered %d: %s", e.op, e.status, e.message)
}

// Is matches the errors package's sentinel for the status.
func (e *gatewayStatusError) Is(target error) bool {
	switch e.status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return target == orerrors.ErrInvalidInput
	case http.StatusUnauthorized:
		return target == orerrors.ErrUnauthorized
	case http.StatusForbidden:
		return target == orerrors.ErrForbidden
	case http.StatusNotFound:
		return target == orerrors.ErrNotFound
	case http.StatusConflict:
		return target == orerrors.ErrConflict
	case http.StatusTooManyRequests:
		return target == orerrors.ErrTooManyRequests
	case http.StatusServiceUnavailable:
		return target == orerrors.ErrServiceUnavailable
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return target == orerrors.ErrTimeout
	}
	return false
}

// statusErrorFrom reads a failed response's body into a *gatewayStatusError
// for op.
func statusErrorFrom(op string, resp *http.Response) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return &gatewayStatusError{op: op, status: resp.StatusCode, message: fmt.Sprintf("(the body could not be read: %v)", err)}
	}
	return &gatewayStatusError{op: op, status: resp.StatusCode, message: gatewayErrorMessage(raw)}
}

// maxErrorBodyBytes bounds how much of a failed response is read for its
// message.
const maxErrorBodyBytes = 64 << 10

// gatewayErrorMessage is what a failing gateway said: the "error" of a JSON
// body, else the body.
func gatewayErrorMessage(raw []byte) string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Error != "" {
		return body.Error
	}
	return strings.TrimSpace(string(raw))
}
