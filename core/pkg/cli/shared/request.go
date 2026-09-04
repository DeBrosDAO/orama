package shared

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// requestTimeout bounds one gateway call. Every endpoint these commands reach
// answers from the local cluster; a request outstanding this long is a gateway
// that is not going to answer.
const requestTimeout = 30 * time.Second

// maxResponse caps what a gateway reply may be read into memory.
const maxResponse = 8 << 20

var httpClient = &http.Client{Timeout: requestTimeout}

// Request performs one authenticated call against the active gateway and
// returns the raw response body.
//
// path is the part after the gateway URL, starting with a slash. body is
// marshalled as JSON when non-nil, and omitted otherwise. The URL and the
// credential come from the same resolution, so a request never carries the key
// stored for a different gateway.
//
// The raw body comes back rather than a decoded value because a command with
// --json prints it verbatim: re-encoding a decoded struct would drop any field
// the CLI does not know about, which is exactly what a caller piping to jq
// needs to still be there.
func Request(method, path string, body any) ([]byte, error) {
	gatewayURL, err := GetAPIURL()
	if err != nil {
		return nil, err
	}
	token, err := GetAuthToken()
	if err != nil {
		return nil, err
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, gatewayURL+path, payload)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, gatewayURL+path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &StatusError{Status: resp.StatusCode, Message: gatewayMessage(raw)}
	}
	return raw, nil
}

// StatusError is a gateway reply that was not a success.
//
// The status is kept alongside the message because callers act on it: a domain
// that is not verified yet answers 400, which a poll retries, while a domain
// that does not exist answers 404, which it must not.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("gateway returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// StatusOf returns the HTTP status an error carries, or 0 if it carries none.
func StatusOf(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	return 0
}

// gatewayMessage extracts what a failing gateway said.
//
// These endpoints answer with http.Error, which writes plain text, but others
// answer with a JSON object carrying "error" or "message". Both shapes reach
// the operator as the same one-line reason.
func gatewayMessage(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}

	var obj struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if obj.Error != "" {
			return obj.Error
		}
		if obj.Message != "" {
			return obj.Message
		}
	}
	return trimmed
}
