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
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push"
	"go.uber.org/zap"
)

// topicFingerprint returns a short, non-reversible identifier for an ntfy topic
// suitable for logs. The full topic is a per-user push channel: in ntfy's model
// knowing the topic name is enough to subscribe to (read) it, so we never write
// it to logs. The fingerprint still lets an operator correlate repeated failures
// for the same topic without exposing the subscribable identifier (bugboard #858).
func topicFingerprint(topic string) string {
	sum := sha256.Sum256([]byte(topic))
	return hex.EncodeToString(sum[:])[:12]
}

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

	// FanoutResolver, when set, returns the set of ntfy publish base URLs to
	// deliver EACH publish to — one per active push node. The cluster runs an
	// independent ntfy per node with NO shared message store, while subscribers
	// are scattered across nodes by round-robin DNS; a publish that lands on one
	// node only reaches subscribers on that node, losing ~(N-1)/N (bugboard
	// #858). Fanning a publish to EVERY node guarantees it reaches whichever
	// instance the subscriber's connection landed on. When nil, or it returns no
	// hosts (or errors), Send falls back to the single BaseURL — so push never
	// breaks if node discovery is unavailable.
	FanoutResolver func(ctx context.Context) ([]string, error)
	// FanoutHostHeader, when set, overrides the HTTP Host header and TLS SNI on
	// fan-out requests. Needed because FanoutResolver returns per-node addresses
	// (IPs) but each node's reverse proxy (Caddy) routes by — and serves its TLS
	// cert for — the public push hostname. Empty: no override (tests /
	// homogeneous hosts).
	FanoutHostHeader string
}

// Provider is the ntfy push.PushProvider implementation.
type Provider struct {
	baseURL          string
	authToken        string
	httpClient       *http.Client
	fanoutClient     *http.Client
	fanoutResolver   func(ctx context.Context) ([]string, error)
	fanoutHostHeader string
	logger           *zap.Logger
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
	p := &Provider{
		baseURL:          strings.TrimRight(cfg.BaseURL, "/"),
		authToken:        cfg.AuthToken,
		httpClient:       &http.Client{Timeout: timeout},
		fanoutResolver:   cfg.FanoutResolver,
		fanoutHostHeader: cfg.FanoutHostHeader,
		logger:           logger.Named("ntfy"),
	}
	if cfg.FanoutResolver != nil {
		// Fan-out requests dial per-node addresses but must present the public
		// push hostname for SNI so each node's Caddy serves the right cert and
		// routes to its local ntfy. A dedicated client carries that fixed SNI.
		tr := &http.Transport{}
		if cfg.FanoutHostHeader != "" {
			tr.TLSClientConfig = &tls.Config{ServerName: cfg.FanoutHostHeader}
		}
		p.fanoutClient = &http.Client{Timeout: timeout, Transport: tr}
	}
	return p
}

// Name implements push.PushProvider.
func (p *Provider) Name() string { return "ntfy" }

// Send delivers a push notification to the device's ntfy topic.
//
// When a FanoutResolver is configured, the publish is delivered to EVERY active
// push node (the ntfy instances don't share state, so the subscriber's instance
// — whichever the round-robin LB picked — must be among the targets), and Send
// succeeds as long as at least one instance accepted it (bugboard #858).
// Otherwise it publishes to the single configured BaseURL.
func (p *Provider) Send(ctx context.Context, msg push.PushMessage) error {
	if msg.DeviceToken == "" {
		return push.ErrEmptyToken
	}
	if p.baseURL == "" {
		return fmt.Errorf("ntfy: base URL not configured")
	}

	topic, err := p.resolveTopic(msg.DeviceToken)
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

	// Resolve the set of base URLs to publish to. Default: the single base URL.
	// With a fan-out resolver, publish to every active push node so the
	// subscriber's instance is always covered. Resolver failure is non-fatal —
	// fall back to the base URL so push keeps working.
	bases := []string{p.baseURL}
	httpClient := p.httpClient
	hostHeader := ""
	if p.fanoutResolver != nil {
		if hosts, rerr := p.fanoutResolver(ctx); rerr != nil {
			p.logger.Warn("ntfy fan-out node resolution failed; publishing to base URL only", zap.Error(rerr))
		} else if len(hosts) > 0 {
			bases = hosts
			httpClient = p.fanoutClient
			hostHeader = p.fanoutHostHeader
		}
	}

	if len(bases) == 1 {
		return p.postOne(ctx, httpClient, bases[0], topic, body, msg, hostHeader)
	}

	// Fan out concurrently. Success = at least one instance accepted the
	// publish (the message is in the cluster). A node that's down is logged but
	// does not fail the Send, since the message still reaches every reachable
	// instance — including, in the common case, the subscriber's.
	var wg sync.WaitGroup
	errs := make([]error, len(bases))
	for i, base := range bases {
		wg.Add(1)
		go func(i int, base string) {
			defer wg.Done()
			errs[i] = p.postOne(ctx, httpClient, base, topic, body, msg, hostHeader)
		}(i, base)
	}
	wg.Wait()

	okCount := 0
	var firstErr error
	var failedNodes []string
	for i, e := range errs {
		if e == nil {
			okCount++
		} else {
			failedNodes = append(failedNodes, bases[i])
			if firstErr == nil {
				firstErr = e
			}
		}
	}
	if okCount == 0 {
		return fmt.Errorf("ntfy: fan-out to all %d push nodes failed: %w", len(bases), firstErr)
	}
	if okCount < len(bases) {
		// bugboard #858: name the failed nodes + topic. The publish succeeds
		// overall (some node accepted), but a subscriber whose round-robin
		// stream is pinned to one of these failed nodes will silently miss this
		// message — exactly the open-subscriber-receives-nothing signature. This
		// makes such a loss diagnosable from the gateway log instead of invisible.
		p.logger.Warn("ntfy fan-out partial failure — a subscriber pinned to a failed node misses this message",
			zap.String("topic_fp", topicFingerprint(topic)),
			zap.Int("delivered", okCount), zap.Int("total", len(bases)),
			zap.Strings("failed_nodes", failedNodes),
			zap.Error(firstErr))
	}
	return nil
}

// postOne publishes a single (already-resolved) topic+body to one ntfy base URL.
// hostHeader, when non-empty, overrides the HTTP Host header so a request dialed
// at a node IP is still routed by the node's proxy as the public push hostname.
func (p *Provider) postOne(ctx context.Context, httpClient *http.Client, base, topic, body string, msg push.PushMessage, hostHeader string) error {
	endpointURL := strings.TrimRight(base, "/") + "/" + topic
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("ntfy: build request: %w", err)
	}
	if hostHeader != "" {
		req.Host = hostHeader
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

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ntfy: http %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	// Drain body to allow connection reuse.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return nil
}

// resolveTopic maps a device token to the escaped ntfy topic path (without the
// base URL), so the same topic can be published to one or many push nodes.
//
// The token is one of two shapes:
//
//   - A plain ntfy topic (possibly hierarchical, e.g. "ns/myapp/user-1") —
//     each path segment is escaped so a crafted token can't break out of the
//     topic path.
//   - A full UnifiedPush endpoint URL handed to the client by the ntfy
//     distributor (e.g. "https://push.example.com/up<random>"). UnifiedPush
//     requires the application server to POST to that endpoint, so we accept it
//     — but ONLY after verifying its scheme+host match the configured base URL,
//     then take only its path as the topic. That turns a device-supplied token
//     into a publish only against our own push host, never an arbitrary one.
func (p *Provider) resolveTopic(token string) (string, error) {
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
		// take only the path as the topic, dropping any query/fragment.
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
	return strings.Join(parts, "/"), nil
}

// isAbsoluteHTTPURL reports whether s looks like an absolute http(s) URL (the
// UnifiedPush endpoint form) rather than a bare ntfy topic.
func isAbsoluteHTTPURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
