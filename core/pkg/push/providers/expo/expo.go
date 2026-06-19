// Package expo wraps the Expo push relay as a push.PushProvider.
//
// This is a thin port of the legacy gateway.PushNotificationService —
// behaviour preserved, surface adapted to the provider abstraction.
//
// Long term Expo is intended to be replaced with direct APNs (iOS) +
// ntfy (Android). This provider exists so the gateway can keep using
// Expo while the migration happens.
package expo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/push"
	"go.uber.org/zap"
)

const expoAPIURL = "https://exp.host/--/api/v2/push/send"

// Config holds Expo provider settings.
type Config struct {
	// AccessToken is an optional Expo access token. When set, it's sent
	// as a Bearer token, which Expo uses for higher-priority delivery
	// and to attribute the send to your account.
	AccessToken string
	// Timeout bounds each Send call. 0 selects 10 seconds (matching the
	// previous PushNotificationService default).
	Timeout time.Duration
}

// Provider is the Expo push.PushProvider implementation.
type Provider struct {
	accessToken string
	httpClient  *http.Client
	logger      *zap.Logger
}

// New creates a Provider with the given config.
func New(cfg Config, logger *zap.Logger) *Provider {
	if logger == nil {
		logger = zap.NewNop()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Provider{
		accessToken: cfg.AccessToken,
		httpClient:  &http.Client{Timeout: timeout},
		logger:      logger.Named("expo"),
	}
}

// Name implements push.PushProvider.
func (p *Provider) Name() string { return "expo" }

// expoMessage matches the wire format Expo expects.
type expoMessage struct {
	To             string                 `json:"to"`
	Title          string                 `json:"title,omitempty"`
	Body           string                 `json:"body,omitempty"`
	Data           map[string]interface{} `json:"data,omitempty"`
	Sound          string                 `json:"sound,omitempty"`
	Badge          int                    `json:"badge,omitempty"`
	Priority       string                 `json:"priority,omitempty"`
	MutableContent bool                   `json:"mutableContent,omitempty"`
	ChannelID      string                 `json:"channelId,omitempty"`
	CollapseID     string                 `json:"collapseId,omitempty"`
}

// expoTicket is the per-message response.
type expoTicket struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type expoResponse struct {
	Data []expoTicket `json:"data"`
}

// Send delivers a push via the Expo relay.
func (p *Provider) Send(ctx context.Context, msg push.PushMessage) error {
	if msg.DeviceToken == "" {
		return push.ErrEmptyToken
	}

	priority := "default"
	if msg.Priority == push.PriorityHigh {
		priority = "high"
	}

	wire := expoMessage{
		To:             msg.DeviceToken,
		Title:          msg.Title,
		Body:           msg.Body,
		Data:           msg.Data,
		Sound:          msg.Sound,
		Badge:          msg.Badge,
		Priority:       priority,
		MutableContent: true, // for iOS Notification Service Extension
		ChannelID:      msg.Channel,
		CollapseID:     msg.MessageID, // bugboard #833: Expo maps collapseId → FCM collapse_key / APNs apns-collapse-id
	}
	if wire.Sound == "" {
		wire.Sound = "default"
	}

	body, err := json.Marshal(wire)
	if err != nil {
		return fmt.Errorf("expo: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, expoAPIURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("expo: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if p.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.accessToken)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("expo: post: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return fmt.Errorf("expo: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expo: http %d: %s", resp.StatusCode, string(respBody))
	}

	var er expoResponse
	if err := json.Unmarshal(respBody, &er); err != nil {
		// Older Expo responses sometimes return a bare array; try that fallback.
		var tickets []expoTicket
		if err2 := json.Unmarshal(respBody, &tickets); err2 == nil {
			er.Data = tickets
		} else {
			return fmt.Errorf("expo: parse response: %w", err)
		}
	}

	for _, t := range er.Data {
		if t.Status != "" && t.Status != "ok" {
			return fmt.Errorf("expo: ticket status %q: %s", t.Status, t.Message)
		}
	}

	return nil
}
