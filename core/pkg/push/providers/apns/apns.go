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
var ErrDeviceUnregistered = errors.New("apns: device token unregistered (410); remove from device store")

// Send delivers one push to the APNs server. Constructs the APNs
// JSON payload from PushMessage, dispatches via the sideshow/apns2
// client, and maps response codes to errors.
func (p *Provider) Send(ctx context.Context, msg push.PushMessage) error {
	if msg.DeviceToken == "" {
		return push.ErrEmptyToken
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
		return fmt.Errorf("apns: push: %w", sendErr)
	}
	if resp == nil {
		return fmt.Errorf("apns: nil response")
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusGone:
		// 410 Unregistered — surfaced as a sentinel so the dispatcher
		// (or caller) can remove the device row.
		return fmt.Errorf("%w: apns_id=%s reason=%s", ErrDeviceUnregistered, resp.ApnsID, resp.Reason)
	default:
		return fmt.Errorf("apns: http %d: reason=%s apns_id=%s",
			resp.StatusCode, resp.Reason, resp.ApnsID)
	}
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
	root := map[string]interface{}{"aps": aps}
	for k, v := range msg.Data {
		// Don't allow tenant data to clobber `aps`.
		if k == "aps" {
			continue
		}
		root[k] = v
	}
	return json.Marshal(root)
}
