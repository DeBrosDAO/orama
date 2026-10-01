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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/netguard"
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

// FanoutPathPrefix is the route on a node's internal gateway that relays a
// fanned-out publish to that node's loopback ntfy; the topic follows it. Served
// over the WireGuard overlay, behind a coordination MAC.
const FanoutPathPrefix = "/v1/internal/push/ntfy/"

// FanoutTarget is one push node a publish is fanned out to.
type FanoutTarget struct {
	// NodeID is the node's libp2p peer id: the audience its coordination MAC is
	// signed for.
	NodeID string
	// BaseURL is the node's internal gateway on the WireGuard overlay
	// (e.g. "http://10.0.0.2:10104").
	BaseURL string
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

	// FanoutResolver, when set, returns the set of push nodes to deliver EACH
	// publish to. The cluster runs an independent ntfy per node with NO shared
	// message store, while subscribers are scattered across nodes by
	// round-robin DNS; a publish that lands on one node only reaches
	// subscribers on that node, losing ~(N-1)/N (bugboard #858). Fanning a
	// publish to EVERY node guarantees it reaches whichever instance the
	// subscriber's connection landed on.
	//
	// Node-to-node traffic travels the WireGuard overlay: each target is a
	// node's internal gateway, which relays the publish to its own loopback
	// ntfy (see FanoutPathPrefix). A resolver error, or an empty set, fails the
	// Send: there is no fallback to the public push host.
	FanoutResolver func(ctx context.Context) ([]FanoutTarget, error)
	// FanoutSigner stamps a fan-out request as coming from inside the cluster,
	// for the node named by nodeID. Required whenever FanoutResolver is set.
	FanoutSigner func(req *http.Request, nodeID string) error

	// GuardTarget is set when BaseURL was supplied by a tenant. Every connection is then checked
	// against the reserved-range list after name resolution (so a name that resolves, or is rebound,
	// to an internal address is refused) and redirects are not followed. The operator's own default
	// is left unguarded: it is the loopback ntfy.
	GuardTarget bool
}

// Provider is the ntfy push.PushProvider implementation.
type Provider struct {
	baseURL        string
	authToken      string
	httpClient     *http.Client
	fanoutResolver func(ctx context.Context) ([]FanoutTarget, error)
	fanoutSigner   func(req *http.Request, nodeID string) error
	logger         *zap.Logger
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
		baseURL:        strings.TrimRight(cfg.BaseURL, "/"),
		authToken:      cfg.AuthToken,
		httpClient:     &http.Client{Timeout: timeout},
		fanoutResolver: cfg.FanoutResolver,
		fanoutSigner:   cfg.FanoutSigner,
		logger:         logger.Named("ntfy"),
	}
	if cfg.GuardTarget {
		guarded := netguard.NewHTTPClient(timeout)
		guarded.CheckRedirect = func(*http.Request, []*http.Request) error {
			return fmt.Errorf("ntfy: a tenant server may not redirect the gateway")
		}
		p.httpClient = guarded
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

	if p.fanoutResolver != nil {
		return p.sendFanout(ctx, topic, body, msg)
	}
	return p.postOne(ctx, p.baseURL+"/"+topic, nil, "", body, msg)
}

// sendFanout publishes to every active push node over the overlay. Success
// means at least one node accepted the publish (the message is in the cluster);
// a node that is down is logged but does not fail the Send, since the message
// still reaches every reachable instance, including, in the common case, the
// subscriber's. A resolver failure, no nodes, or every node refusing is an
// error: the push reached nobody.
func (p *Provider) sendFanout(ctx context.Context, topic, body string, msg push.PushMessage) error {
	if p.fanoutSigner == nil {
		return fmt.Errorf("ntfy: fan-out is configured without a signer, so no node would accept the publish")
	}
	targets, err := p.fanoutResolver(ctx)
	if err != nil {
		return fmt.Errorf("ntfy: resolve push nodes for fan-out: %w", err)
	}
	if len(targets) == 0 {
		return fmt.Errorf("ntfy: no active push nodes to fan the publish out to")
	}

	var wg sync.WaitGroup
	errs := make([]error, len(targets))
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t FanoutTarget) {
			defer wg.Done()
			sign := func(req *http.Request) error { return p.fanoutSigner(req, t.NodeID) }
			errs[i] = p.postOne(ctx, strings.TrimRight(t.BaseURL, "/")+FanoutPathPrefix+topic, sign, t.NodeID, body, msg)
		}(i, t)
	}
	wg.Wait()

	okCount := 0
	var firstErr error
	var failedNodes []string
	for i, e := range errs {
		if e == nil {
			okCount++
			continue
		}
		failedNodes = append(failedNodes, targets[i].NodeID)
		if firstErr == nil {
			firstErr = e
		}
	}
	if okCount == 0 {
		return fmt.Errorf("ntfy: fan-out to all %d push nodes failed: %w", len(targets), firstErr)
	}
	if okCount < len(targets) {
		// bugboard #858: name the failed nodes + topic. A subscriber whose
		// round-robin stream is pinned to one of these nodes will silently miss
		// this message; this makes such a loss diagnosable from the gateway log.
		p.logger.Warn("ntfy fan-out partial failure — a subscriber pinned to a failed node misses this message",
			zap.String("topic_fp", topicFingerprint(topic)),
			zap.Int("delivered", okCount), zap.Int("total", len(targets)),
			zap.Strings("failed_nodes", failedNodes),
			zap.Error(firstErr))
	}
	return nil
}

// postOne publishes a single (already-resolved) topic+body to endpointURL. A
// non-nil sign stamps the request as a cluster-internal one (fan-out); the ntfy
// bearer token is sent only on a direct publish, since the node-local ntfy
// behind the internal route is loopback-only and unauthenticated.
func (p *Provider) postOne(ctx context.Context, endpointURL string, sign func(*http.Request) error, node, body string, msg push.PushMessage) error {
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
	if sign != nil {
		if err := sign(req); err != nil {
			return fmt.Errorf("ntfy: sign fan-out request for node %s: %w", node, err)
		}
	} else if p.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.authToken)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: post: %w", push.RedactRequestURL(err))
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
