package apns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/token"
	"go.uber.org/zap"
)

// defaultSendTimeout bounds each apns.Push call. APNs is usually <100ms
// but mobile networks + Apple-side slowness occasionally push to seconds.
// 10 seconds is a comfortable upper bound; faster than the legacy ntfy
// provider's 5s because APNs is HTTP/2 + connection-reused.
const defaultSendTimeout = 10 * time.Second

// Provider is the APNs push.PushProvider implementation, scoped to one
// (Team ID, Key ID, p8 key, Bundle ID, Environment) tuple. Construct
// one per namespace via the gateway dependency factory.
type Provider struct {
	bundleID string
	client   pushClient
	logger   *zap.Logger
}

// pushClient is the subset of *apns2.Client this provider uses,
// extracted so tests can substitute a fake without spinning up an HTTPS
// server with a self-signed APNs cert.
//
// We use PushWithContext (not Push) so context cancellation actually
// reaches the underlying HTTP/2 stream — otherwise an abandoned ctx
// leaves the request running until apns2's internal HTTPClient.Timeout
// fires, leaking a goroutine and a connection per cancelled send.
//
// The first arg is `apns2.Context` (which embeds context.Context) to
// match the upstream signature exactly — any standard context.Context
// satisfies apns2.Context's single-method interface.
type pushClient interface {
	PushWithContext(ctx apns2.Context, notification *apns2.Notification) (*apns2.Response, error)
}

// New constructs a Provider from a parsed Config. Returns an error if
// the p8 key fails to parse — this surfaces config errors at gateway
// startup / first-send rather than at every Push call.
func New(c Config, logger *zap.Logger) (*Provider, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if err := validateConfig(c); err != nil {
		return nil, err
	}
	authKey, err := token.AuthKeyFromBytes([]byte(c.P8Key))
	if err != nil {
		return nil, fmt.Errorf("apns: parse p8 key: %w", err)
	}
	tok := &token.Token{
		AuthKey: authKey,
		KeyID:   c.KeyID,
		TeamID:  c.TeamID,
	}
	client := apns2.NewTokenClient(tok)
	switch c.Environment {
	case EnvProduction:
		client = client.Production()
	case EnvSandbox:
		client = client.Development()
	default:
		// validateConfig already rejected anything else.
		return nil, fmt.Errorf("apns: unsupported environment %q", c.Environment)
	}
	// Override the underlying HTTP/2 client's per-request timeout. apns2's
	// default of zero means "no timeout" — bad for a server-side context.
	client.HTTPClient.Timeout = defaultSendTimeout
	return &Provider{
		bundleID: c.BundleID,
		client:   client,
		logger:   logger.Named("apns"),
	}, nil
}

// Name implements push.PushProvider.
func (p *Provider) Name() string { return "apns" }

// ErrDeviceUnregistered is returned by Send when APNs responds with
// "Unregistered" (HTTP 410) — the token is no longer valid because the
// user uninstalled the app, disabled notifications, or upgraded device.
// Callers SHOULD delete the device row when they see this so the same
// dead token doesn't get retried forever.
//
// Kept as an exported sentinel for backwards compatibility — callers
// that want the structured shape should use errors.As(err, &push.PushError{})
// and check the Unregistered field.
var ErrDeviceUnregistered = errors.New("apns: device token unregistered (410); remove from device store")

// Send delivers one push to the APNs server. Constructs the APNs
// JSON payload from PushMessage, dispatches via the sideshow/apns2
// client, and maps response codes to errors.
//
// Returns nil on HTTP 200, *push.PushError on any HTTP response APNs
// gave us (status, reason, unregistered-flag baked in), or a plain
// wrapped error for transport/validation failures (no HTTP response).
//
// Bugboard #348 root-cause guard: rejects empty visible-content
// payloads up-front (no title, no body, no badge, no sound, no
// content-available) — Apple silently 200s those AND drops them
// without displaying, which previously looked like a successful
// delivery to the WASM caller. We surface the failure here so it
// doesn't look like success.
func (p *Provider) Send(ctx context.Context, msg push.PushMessage) error {
	if msg.DeviceToken == "" {
		return push.ErrEmptyToken
	}
	if !hasVisibleContent(msg) {
		return push.ErrEmptyContent
	}
	payload, err := buildAPSPayload(msg)
	if err != nil {
		return fmt.Errorf("apns: build payload: %w", err)
	}
	n := &apns2.Notification{
		DeviceToken: msg.DeviceToken,
		Topic:       p.bundleID,
		Payload:     payload,
	}
	// Priority mapping: APNs uses 10 (immediate) / 5 (power-saving).
	if msg.Priority == push.PriorityHigh {
		n.Priority = apns2.PriorityHigh
	} else {
		n.Priority = apns2.PriorityLow
	}

	// PushWithContext propagates cancellation through to the HTTP/2
	// stream — abandoning ctx terminates the in-flight request, no
	// goroutine leak.
	resp, sendErr := p.client.PushWithContext(ctx, n)
	if sendErr != nil {
		// Transport-level failure (network, ctx cancel, etc.) — no
		// HTTP response to dissect. Plain wrap so callers can still
		// errors.Is against the underlying.
		return fmt.Errorf("apns: push: %w", sendErr)
	}
	if resp == nil {
		return fmt.Errorf("apns: nil response")
	}

	// Always log the APNs HTTP response so we have visibility into
	// silent-drop classes (Apple 200 + no delivery, throttling, etc.).
	// Bugboard #348 diagnostic — see investigation comment.
	p.logger.Info("apns send response",
		zap.Int("http_status", resp.StatusCode),
		zap.String("reason", resp.Reason),
		zap.String("apns_id", resp.ApnsID),
		zap.String("device_token_prefix", tokenPrefix(msg.DeviceToken)),
	)

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusGone:
		// 410 Unregistered — both the sentinel sentinel wrap (for
		// legacy errors.Is callers) AND a structured PushError (for
		// the new SendToUserDetailed dispatcher path).
		return &push.PushError{
			HTTPStatus:   http.StatusGone,
			Reason:       resp.Reason,
			Message:      fmt.Sprintf("apns: device token unregistered (410): apns_id=%s reason=%s", resp.ApnsID, resp.Reason),
			Unregistered: true,
			Wrapped:      ErrDeviceUnregistered,
		}
	default:
		return &push.PushError{
			HTTPStatus: resp.StatusCode,
			Reason:     resp.Reason,
			Message:    fmt.Sprintf("apns: http %d: reason=%s apns_id=%s", resp.StatusCode, resp.Reason, resp.ApnsID),
		}
	}
}

// hasVisibleContent reports whether the message has any payload field
// that Apple will display or process. An APNs push with none of these
// is silently 200'd by Apple AND dropped — that's the bugboard #348
// root cause we want to surface as a structured error.
//
// `content_available: true` in Data signals a background-only push
// (legal even with empty alert) — we accept that as valid content.
func hasVisibleContent(msg push.PushMessage) bool {
	if msg.Title != "" || msg.Body != "" {
		return true
	}
	if msg.Badge > 0 {
		return true
	}
	if msg.Sound != "" {
		return true
	}
	if ca, ok := msg.Data["content_available"]; ok {
		// Accept truthy variants: bool true, int/float != 0, "1"/"true".
		switch v := ca.(type) {
		case bool:
			return v
		case int:
			return v != 0
		case int64:
			return v != 0
		case float64:
			return v != 0
		case string:
			return v == "1" || v == "true"
		}
	}
	return false
}

// tokenPrefix returns the first 8 chars of a device token, safe for
// logging. The full token is sensitive — never log it whole.
func tokenPrefix(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8] + "..."
}

// buildAPSPayload assembles the APNs JSON payload from a generic
// PushMessage. The `aps` dictionary is the Apple-required wrapper;
// custom fields (`data`) go alongside at the top level.
//
// Reference: https://developer.apple.com/documentation/usernotifications/setting_up_a_remote_notification_server/generating_a_remote_notification
func buildAPSPayload(msg push.PushMessage) ([]byte, error) {
	alert := map[string]string{}
	if msg.Title != "" {
		alert["title"] = msg.Title
	}
	if msg.Body != "" {
		alert["body"] = msg.Body
	}
	aps := map[string]interface{}{}
	if len(alert) > 0 {
		aps["alert"] = alert
	}
	if msg.Badge > 0 {
		aps["badge"] = msg.Badge
	}
	if msg.Sound != "" {
		aps["sound"] = msg.Sound
	}
	if msg.Channel != "" {
		// Apple's "thread-id" groups notifications into a conversation in
		// the lock-screen view. Channel is the most natural mapping.
		aps["thread-id"] = msg.Channel
	}
	// content-available: 1 signals a background-only push to iOS. The
	// caller opts in via Data["content_available"] (any truthy value).
	// Mapped here at the aps boundary so the WASM Data shape stays
	// snake_case while Apple's wire format uses the canonical key.
	if ca, ok := msg.Data["content_available"]; ok {
		switch v := ca.(type) {
		case bool:
			if v {
				aps["content-available"] = 1
			}
		case int:
			if v != 0 {
				aps["content-available"] = 1
			}
		case int64:
			if v != 0 {
				aps["content-available"] = 1
			}
		case float64:
			if v != 0 {
				aps["content-available"] = 1
			}
		case string:
			if v == "1" || v == "true" {
				aps["content-available"] = 1
			}
		}
	}
	root := map[string]interface{}{"aps": aps}
	for k, v := range msg.Data {
		// Don't allow tenant data to clobber `aps`, and skip the
		// content_available marker since we mapped it to aps above.
		if k == "aps" || k == "content_available" {
			continue
		}
		root[k] = v
	}
	return json.Marshal(root)
}
