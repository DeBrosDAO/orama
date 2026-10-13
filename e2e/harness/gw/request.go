package gw

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// Req describes one request. Header values are sent as given, so a negative
// test can send a header twice (Header["X-Api-Key"] = []string{"a", "b"}).
type Req struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	// Body is sent verbatim, malformed or not.
	Body []byte
	// Bearer sets Authorization: Bearer <token>; APIKey sets X-API-Key.
	Bearer string
	APIKey string
	// Host overrides the Host header (virtual-host routing tests).
	Host string
}

// Send builds and sends r.
func (c *Client) Send(ctx context.Context, r Req) (*Response, error) {
	req, err := c.newRequest(ctx, r)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// newRequest builds r against the client's base URL.
func (c *Client) newRequest(ctx context.Context, r Req) (*http.Request, error) {
	u := c.BaseURL + r.Path
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(r.Body))
	if err != nil {
		return nil, fmt.Errorf("failed to build %s %s: %w", method, r.Path, err)
	}
	if r.Body == nil {
		req.Body, req.GetBody, req.ContentLength = http.NoBody, nil, 0
	}
	for k, vs := range r.Header {
		req.Header[k] = append([]string{}, vs...)
	}
	if r.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+r.Bearer)
	}
	if r.APIKey != "" {
		req.Header.Set("X-API-Key", r.APIKey)
	}
	if r.Host != "" {
		req.Host = r.Host
	}
	return req, nil
}

// JSON sends in (when non-nil) as a JSON body with bearer auth and decodes a
// 2xx response into out (when non-nil). A non-2xx response is returned with a
// *StatusError so callers can assert on the refusal.
func (c *Client) JSON(ctx context.Context, method, path, bearer string, in, out any) (*Response, error) {
	r := Req{Method: method, Path: path, Bearer: bearer, Header: http.Header{}}
	if in != nil {
		body, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("failed to encode %s %s body: %w", method, path, err)
		}
		r.Body = body
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Send(ctx, r)
	if err != nil {
		return resp, err
	}
	if resp.Status < 200 || resp.Status > 299 {
		return resp, &StatusError{Method: method, Path: path, Status: resp.Status, Body: string(resp.Body)}
	}
	if out != nil {
		if err := resp.Decode(out); err != nil {
			return resp, fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return resp, nil
}

// StatusError is a non-2xx answer.
type StatusError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s answered %d: %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

// Decode unmarshals the body.
func (r *Response) Decode(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("failed to decode %d response as JSON (%q): %w", r.Status, truncate(string(r.Body), 200), err)
	}
	return nil
}

// ErrorCode returns the "code" member of a JSON error body, or "".
func (r *Response) ErrorCode() string {
	var body struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(r.Body, &body) != nil {
		return ""
	}
	return body.Code
}

// Expect fails the test unless the response has status.
func (r *Response) Expect(t testing.TB, status int) *Response {
	t.Helper()
	if r.Status != status {
		t.Fatalf("want HTTP %d, got %d: %s", status, r.Status, truncate(string(r.Body), 2000))
	}
	return r
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// MustSend is Send that fails the test when the request could not be made.
// Called from a t.Cleanup, where t.Context() is already cancelled, it sends
// on a fresh bounded context (fleet.ContextFor) instead of failing at once.
func (c *Client) MustSend(t testing.TB, r Req) *Response {
	t.Helper()
	ctx, cancel := fleet.ContextFor(t)
	defer cancel()
	resp, err := c.For(t).Send(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
