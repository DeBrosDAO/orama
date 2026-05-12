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

	// URL-escape each path segment of the device token. ntfy topics can be
	// hierarchical (e.g. "ns/myapp/user-1") and we want to preserve those
	// '/' separators while escaping any other special characters that
	// could let a malicious token escape the topic path.
	parts := strings.Split(msg.DeviceToken, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	endpointURL := p.baseURL + "/" + strings.Join(parts, "/")

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
