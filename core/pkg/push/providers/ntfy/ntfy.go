// Package ntfy implements a push.PushProvider backed by an ntfy server.
//
// ntfy delivers notifications via plain HTTP POST to <baseURL>/<topic>.
// We map PushMessage fields to ntfy headers:
//   - Title    -> "Title"
//   - Priority -> "Priority"
//   - Channel  -> "Tags"
//   - Data     -> base64-encoded JSON in "X-Data"
//
// See https://docs.ntfy.sh/publish/#publish-as-json for details.
package ntfy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push"
	"go.uber.org/zap"
)

// Config holds per-provider settings.
type Config struct {
	// BaseURL is the ntfy HTTP endpoint (e.g. "http://localhost:8080" or
	// "https://push.example.com"). Trailing slash is tolerated.
	BaseURL string
	// AuthToken is an optional per-namespace bearer token. Leave empty to
	// disable authentication.
	AuthToken string
	// Timeout bounds each Send call. 0 selects 5 seconds.
	Timeout time.Duration
}

// Provider is the ntfy push.PushProvider implementation.
type Provider struct {
	baseURL    string
	authToken  string
	httpClient *http.Client
	logger     *zap.Logger
}

// New creates a Provider with the given config.
func New(cfg Config, logger *zap.Logger) *Provider {
	if logger == nil {
		logger = zap.NewNop()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Provider{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		authToken:  cfg.AuthToken,
		httpClient: &http.Client{Timeout: timeout},
		logger:     logger.Named("ntfy"),
	}
}

// Name implements push.PushProvider.
func (p *Provider) Name() string { return "ntfy" }

// Send delivers a push notification to the device's ntfy topic.
func (p *Provider) Send(ctx context.Context, msg push.PushMessage) error {
	if msg.DeviceToken == "" {
		return push.ErrEmptyToken
	}
	if p.baseURL == "" {
		return fmt.Errorf("ntfy: base URL not configured")
	}

	endpointURL, err := p.resolveEndpoint(msg.DeviceToken)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, strings.NewReader(msg.Body))
	if err != nil {
		return fmt.Errorf("ntfy: build request: %w", err)
	}

	if msg.Title != "" {
		req.Header.Set("Title", msg.Title)
	}
	if msg.Priority == push.PriorityHigh {
		req.Header.Set("Priority", "high")
	} else if msg.Priority == push.PriorityNormal {
		req.Header.Set("Priority", "default")
	}
	if msg.Channel != "" {
		// ntfy uses "Tags" for both visual emoji and operator-defined tags.
		req.Header.Set("Tags", msg.Channel)
	}
	if msg.Badge > 0 {
		req.Header.Set("X-Badge", fmt.Sprintf("%d", msg.Badge))
	}
	if len(msg.Data) > 0 {
		b, err := json.Marshal(msg.Data)
		if err != nil {
			return fmt.Errorf("ntfy: marshal data: %w", err)
		}
		req.Header.Set("X-Data", base64.StdEncoding.EncodeToString(b))
	}
	if p.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.authToken)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ntfy: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Drain body to allow connection reuse.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
}

// resolveEndpoint maps a device token to the ntfy publish URL.
//
// The token is one of two shapes:
//
//   - A plain ntfy topic (possibly hierarchical, e.g. "ns/myapp/user-1") —
//     published to "<baseURL>/<topic>", with each path segment escaped so a
//     crafted token can't break out of the topic path.
//   - A full UnifiedPush endpoint URL handed to the client by the ntfy
//     distributor (e.g. "https://push.example.com/up<random>"). UnifiedPush
//     requires the application server to POST to that endpoint verbatim, so we
//     use it as-is — but ONLY after verifying its scheme+host match the
//     configured base URL. That check turns a device-supplied token into an
//     SSRF only against our own push host, never an arbitrary one.
func (p *Provider) resolveEndpoint(token string) (string, error) {
	topic := token
	if isAbsoluteHTTPURL(token) {
		u, err := url.Parse(token)
		if err != nil {
			return "", fmt.Errorf("ntfy: invalid endpoint url: %w", err)
		}
		base, err := url.Parse(p.baseURL)
		if err != nil {
			return "", fmt.Errorf("ntfy: invalid base url %q: %w", p.baseURL, err)
		}
		if !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
			// Reject an endpoint pointing anywhere other than the configured
			// push host — a device token must never become an SSRF vector.
			return "", fmt.Errorf("ntfy: endpoint host %q does not match configured push host %q", u.Host, base.Host)
		}
		// Confine the URL form to the SAME publish surface as a bare topic:
		// take only the path as the topic and re-build through the per-segment
		// escaping below, dropping any query/fragment. So a UnifiedPush
		// endpoint token can publish a topic but can't gain arbitrary path or
		// query control on the push host beyond what a plain topic already has.
		topic = strings.TrimPrefix(u.Path, "/")
		if topic == "" {
			return "", fmt.Errorf("ntfy: endpoint url %q has no topic path", token)
		}
	}

	// Escape each path segment, preserving the '/' hierarchy.
	parts := strings.Split(topic, "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return p.baseURL + "/" + strings.Join(parts, "/"), nil
}

// isAbsoluteHTTPURL reports whether s looks like an absolute http(s) URL (the
// UnifiedPush endpoint form) rather than a bare ntfy topic.
func isAbsoluteHTTPURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
