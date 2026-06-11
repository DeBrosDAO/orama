// Package ntfy implements a push.PushProvider backed by an ntfy server.
//
// ntfy delivers notifications via plain HTTP POST to <baseURL>/<topic>.
// We map PushMessage fields to the ntfy publish surface:
//   - Title    -> "Title"  header
//   - Priority -> "Priority" header
//   - Channel  -> "Tags" header
//   - Body     -> the POST body (ntfy's "message", relayed verbatim)
//   - Data     -> the POST body as JSON, ONLY when Body is empty
//
// IMPORTANT (bugboard #126): ntfy does NOT relay arbitrary `X-*` request
// headers into the subscriber stream — only its recognized publish headers
// (Title, Priority, Tags, Click, Actions, Attach, …) and the message body
// reach the client. So structured Data and a numeric Badge cannot be carried
// as custom headers; the only field a subscriber reliably receives besides
// title/priority/tags is the message BODY. We therefore deliver Data through
// the body (UnifiedPush convention: the body IS the payload). A caller that
// sets an explicit Body owns it — to ship structured data alongside a
// human-readable body, encode both into the Body envelope.
//
// See https://docs.ntfy.sh/publish/ for the recognized header set.
package ntfy

import (
	"context"
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

	// Determine the POST body — the only structured payload ntfy relays to
	// subscribers (bugboard #126). A caller-supplied Body wins; otherwise, if
	// there's structured Data, serialize it as the body so a data-only push
	// still reaches the client (UnifiedPush convention: body == payload).
	body := msg.Body
	if body == "" && len(msg.Data) > 0 {
		b, err := json.Marshal(msg.Data)
		if err != nil {
			return fmt.Errorf("ntfy: marshal data: %w", err)
		}
		body = string(b)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, strings.NewReader(body))
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
	// NOTE: Badge and arbitrary Data are intentionally NOT sent as custom
	// headers — ntfy does not relay `X-*` headers to subscribers (#126), so
	// doing so silently drops them. Data rides the body (above); a badge
	// count, if needed, must be encoded into the body by the caller.
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
