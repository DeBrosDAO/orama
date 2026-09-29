// Package cloudflare writes and removes the DNS delegation of e2e runs in the
// one Cloudflare zone they may use. It refuses any other zone, and any record
// name that is not inside a run subdomain (e2e-<id>.<zone>), so a mistake in
// the harness cannot touch the zone's own records.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the public API.
	DefaultBaseURL = "https://api.cloudflare.com/client/v4"
	// AllowedZone is the only zone e2e runs may write.
	AllowedZone = "dbrsteting.bid"
	// requestTimeout bounds one HTTP exchange.
	requestTimeout = 30 * time.Second
	// maxBodyBytes caps a response body.
	maxBodyBytes = 4 << 20
	// perPage is the page size of record listings.
	perPage = 100
)

// Client manages records of AllowedZone.
type Client struct {
	token  string
	zone   string
	base   string
	http   *http.Client
	zoneID string
}

// New returns a client for zone, which must be AllowedZone. The token is
// used only in the Authorization header and never appears in an error.
func New(token, zone, baseURL string) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("cloudflare: the API token is empty (set CF_API_TOKEN)")
	}
	z := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), ".")
	if !strings.Contains(z, ".") {
		return nil, fmt.Errorf("cloudflare: zone %q is a bare TLD; e2e runs only write %s", zone, AllowedZone)
	}
	if z != AllowedZone {
		return nil, fmt.Errorf("cloudflare: zone %q is not %s, the only zone e2e runs may write (check CF_ZONE)", zone, AllowedZone)
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{token: token, zone: z, base: strings.TrimSuffix(baseURL, "/"), http: &http.Client{Timeout: requestTimeout}}, nil
}

// Zone is the zone this client writes.
func (c *Client) Zone() string { return c.zone }

// Record is one DNS record.
type Record struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Name       string    `json:"name"`
	Content    string    `json:"content"`
	CreatedOn  time.Time `json:"created_on"`
	ModifiedOn time.Time `json:"modified_on"`
}

type envelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

// ZoneID looks the zone up once and caches its id.
func (c *Client) ZoneID(ctx context.Context) (string, error) {
	if c.zoneID != "" {
		return c.zoneID, nil
	}
	var zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if _, err := c.do(ctx, "GET", "/zones?"+url.Values{"name": {c.zone}}.Encode(), nil, &zones); err != nil {
		return "", fmt.Errorf("failed to look up zone %s: %w", c.zone, err)
	}
	for _, z := range zones {
		if z.Name == c.zone {
			c.zoneID = z.ID
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("cloudflare has no zone %s visible to this token (it needs Zone:Read on it)", c.zone)
}

// do sends one request and decodes the envelope's result into out.
func (c *Client) do(ctx context.Context, method, path string, body, out any) (envelope, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return envelope{}, fmt.Errorf("cloudflare %s %s: failed to encode the request: %w", method, path, err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return envelope{}, fmt.Errorf("cloudflare %s %s: failed to build the request: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return envelope{}, fmt.Errorf("cloudflare %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return envelope{}, fmt.Errorf("cloudflare %s %s: failed to read the answer: %w", method, path, err)
	}
	return decode(method, path, resp.StatusCode, raw, out)
}

// decode checks the status and envelope and unmarshals the result.
func decode(method, path string, status int, raw []byte, out any) (envelope, error) {
	var env envelope
	jsonErr := json.Unmarshal(raw, &env)
	if status < 200 || status >= 300 || jsonErr != nil || !env.Success {
		return env, &APIError{Method: method, Path: path, Status: status, Messages: messages(env, raw)}
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return env, fmt.Errorf("cloudflare %s %s: failed to decode the result: %w", method, path, err)
		}
	}
	return env, nil
}

func messages(env envelope, raw []byte) string {
	var parts []string
	for _, e := range env.Errors {
		parts = append(parts, strconv.Itoa(e.Code)+" "+e.Message)
	}
	if len(parts) == 0 {
		text := strings.TrimSpace(string(raw))
		if len(text) > 200 {
			text = text[:200]
		}
		return text
	}
	return strings.Join(parts, "; ")
}

// APIError is a failed Cloudflare call.
type APIError struct {
	Method, Path string
	Status       int
	Messages     string
}

func (e *APIError) Error() string {
	base := fmt.Sprintf("cloudflare %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, e.Messages)
	switch e.Status {
	case http.StatusUnauthorized:
		return base + " — CF_API_TOKEN was rejected: it is wrong, expired or revoked"
	case http.StatusForbidden:
		return base + " — the token lacks permission: it needs Zone:Read and DNS:Edit on " + AllowedZone
	}
	return base
}

// IsNotFound reports a 404 answer.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}
