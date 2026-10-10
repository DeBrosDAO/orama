// Package hetzner is the small part of the Hetzner Cloud REST API an e2e run
// needs: servers, SSH keys, firewalls, locations and server types. Every call
// is bounded and follows its context. Nothing is retried except a request the
// API answered 429 with a documented wait (Retry-After or RateLimit-Reset),
// and that at most maxRateLimitRetries times: a failure is reported, never
// hidden.
package hetzner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the public API.
	DefaultBaseURL = "https://api.hetzner.cloud/v1"
	// requestTimeout bounds one HTTP exchange.
	requestTimeout = 30 * time.Second
	// maxBodyBytes caps a response body.
	maxBodyBytes = 4 << 20
	// maxRateLimitRetries bounds the retries of a 429 answer.
	maxRateLimitRetries = 2
	// maxRateLimitWait is the longest documented wait honoured; longer fails.
	maxRateLimitWait = 60 * time.Second
	// codeLimitExceeded is Hetzner's error code for a project quota.
	codeLimitExceeded = "resource_limit_exceeded"
)

// Client talks to one Hetzner Cloud project.
type Client struct {
	token string
	base  string
	http  *http.Client
	// now is the clock RateLimit-Reset is read against.
	now func() time.Time
}

// New returns a client for token. baseURL is empty for the public API.
func New(token, baseURL string) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("hetzner: the API token is empty (set HCLOUD_TOKEN)")
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		token: token,
		base:  strings.TrimSuffix(baseURL, "/"),
		http:  &http.Client{Timeout: requestTimeout},
		now:   time.Now,
	}, nil
}

// APIError is an answer the API gave with a non-2xx status.
type APIError struct {
	Method, Path string
	Status       int
	Code         string
	Message      string
}

func (e *APIError) Error() string {
	base := fmt.Sprintf("hetzner %s %s: HTTP %d %s: %s", e.Method, e.Path, e.Status, e.Code, e.Message)
	switch {
	case e.Status == http.StatusUnauthorized:
		return base + " — HCLOUD_TOKEN was rejected: it is wrong or revoked; create a read/write API token in the project"
	case e.Status == http.StatusForbidden:
		return base + " — the token lacks permission for this call: it must be a read/write token"
	case e.Code == codeLimitExceeded:
		return base + " — the project reached a Hetzner resource limit (servers default to 10): " +
			"delete leftover servers (e2e sweep) or ask Hetzner to raise the limit"
	case e.Status == http.StatusTooManyRequests:
		return base + " — rate limited beyond the documented wait; run again later"
	case e.Status >= http.StatusInternalServerError:
		return base + " — Hetzner server error; it was not retried, run again when the API recovers"
	}
	return base
}

// IsNotFound reports a 404 answer.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// IsLimitExceeded reports a project resource limit answer.
func IsLimitExceeded(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Code == codeLimitExceeded
}

// do sends one request, decoding a 2xx body into out when out is not nil.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("hetzner %s %s: failed to encode the request: %w", method, path, err)
		}
		payload = raw
	}
	for attempt := 0; ; attempt++ {
		resp, raw, err := c.send(ctx, method, path, payload)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRateLimitRetries {
			if wait, ok := c.rateLimitWait(resp.Header); ok {
				if err := waitFor(ctx, wait); err != nil {
					return fmt.Errorf("hetzner %s %s: rate limited and %w", method, path, err)
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return apiError(method, path, resp.StatusCode, raw)
		}
		if out == nil || len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("hetzner %s %s: failed to decode the answer: %w", method, path, err)
		}
		return nil
	}
}

// send performs one HTTP exchange and reads the (capped) body.
func (c *Client) send(ctx context.Context, method, path string, payload []byte) (*http.Response, []byte, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, nil, fmt.Errorf("hetzner %s %s: failed to build the request: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("hetzner %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("hetzner %s %s: failed to read the answer: %w", method, path, err)
	}
	return resp, raw, nil
}

// rateLimitWait is the wait a 429 documents: Retry-After seconds, or the
// RateLimit-Reset unix time. ok is false when neither is usable or the wait
// is longer than maxRateLimitWait.
func (c *Client) rateLimitWait(h http.Header) (time.Duration, bool) {
	var wait time.Duration
	if s, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && s >= 0 {
		wait = time.Duration(s) * time.Second
	} else if ts, err := strconv.ParseInt(strings.TrimSpace(h.Get("RateLimit-Reset")), 10, 64); err == nil {
		wait = time.Unix(ts, 0).Sub(c.now())
		if wait < 0 {
			wait = 0
		}
	} else {
		return 0, false
	}
	return wait, wait <= maxRateLimitWait
}

// waitFor blocks for d or until ctx ends.
func waitFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("the context ended during the documented wait: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

// apiError decodes Hetzner's error envelope.
func apiError(method, path string, status int, raw []byte) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	e := &APIError{Method: method, Path: path, Status: status}
	if err := json.Unmarshal(raw, &env); err == nil && env.Error.Code != "" {
		e.Code, e.Message = env.Error.Code, env.Error.Message
		return e
	}
	e.Code = "unknown"
	e.Message = strings.TrimSpace(string(raw))
	if len(e.Message) > 200 {
		e.Message = e.Message[:200]
	}
	return e
}
