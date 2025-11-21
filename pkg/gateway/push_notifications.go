package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// PushNotificationService handles sending push notifications via Expo
type PushNotificationService struct {
	logger *zap.Logger
	client *http.Client
}

// ExpoTicket represents the response from Expo API
type ExpoTicket struct {
	ID    string `json:"id"`
	Error string `json:"error,omitempty"`
}

// ExpoPushMessage represents a message to send via Expo
type ExpoPushMessage struct {
	To       string                 `json:"to"`
	Title    string                 `json:"title"`
	Body     string                 `json:"body"`
	Data     map[string]interface{} `json:"data,omitempty"`
	Sound    string                 `json:"sound,omitempty"`
	Badge    int                    `json:"badge,omitempty"`
	Priority string                 `json:"priority,omitempty"`
	// iOS specific
	MutableContent bool   `json:"mutableContent,omitempty"`
	IosIcon        string `json:"iosIcon,omitempty"`
	// Android specific
	AndroidBigLargeIcon string `json:"androidBigLargeIcon,omitempty"`
	ChannelID           string `json:"channelId,omitempty"`
}

// NewPushNotificationService creates a new push notification service
func NewPushNotificationService(logger *zap.Logger) *PushNotificationService {
	return &PushNotificationService{
		logger: logger,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SendNotification sends a push notification via Expo
func (pns *PushNotificationService) SendNotification(
	ctx context.Context,
	expoPushToken string,
	title string,
	body string,
	data map[string]interface{},
	avatarURL string,
) error {
	if expoPushToken == "" {
		return fmt.Errorf("empty expo push token")
	}

	message := ExpoPushMessage{
		To:       expoPushToken,
		Title:    title,
		Body:     body,
		Data:     data,
		Sound:    "default",
		Priority: "high",
		// Enable mutable content for iOS to allow Notification Service Extension
		MutableContent:      true,
		ChannelID:           "messages",
		AndroidBigLargeIcon: avatarURL,
	}

	// For iOS, include avatar in data so Notification Service Extension can fetch it
	if avatarURL != "" {
		if message.Data == nil {
			message.Data = make(map[string]interface{})
		}
		message.Data["avatar_url"] = avatarURL
	}

	return pns.sendExpoRequest(ctx, message)
}

// SendBulkNotifications sends notifications to multiple users
func (pns *PushNotificationService) SendBulkNotifications(
	ctx context.Context,
	expoPushTokens []string,
	title string,
	body string,
	data map[string]interface{},
	avatarURL string,
) []error {
	errors := make([]error, 0)

	for _, token := range expoPushTokens {
		if err := pns.SendNotification(ctx, token, title, body, data, avatarURL); err != nil {
			errors = append(errors, fmt.Errorf("failed to send to token %s: %w", token, err))
		}
	}

	return errors
}

// sendExpoRequest sends a request to the Expo push notification API
func (pns *PushNotificationService) sendExpoRequest(ctx context.Context, message ExpoPushMessage) error {
	const expoAPIURL = "https://exp.host/--/api/v2/push/send"

	body, err := json.Marshal(message)
	if err != nil {
		pns.logger.Error("failed to marshal push notification",
			zap.Error(err),
			zap.String("to", message.To))
		return fmt.Errorf("marshal error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, expoAPIURL, bytes.NewBuffer(body))
	if err != nil {
		pns.logger.Error("failed to create push notification request",
			zap.Error(err),
			zap.String("to", message.To))
		return fmt.Errorf("request creation error: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := pns.client.Do(req)
	if err != nil {
		pns.logger.Error("failed to send push notification",
			zap.Error(err),
			zap.String("to", message.To))
		return fmt.Errorf("send error: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		pns.logger.Error("failed to read push notification response",
			zap.Error(err),
			zap.String("to", message.To))
		return fmt.Errorf("response read error: %w", err)
	}

	// Check for API errors
	if resp.StatusCode != http.StatusOK {
		pns.logger.Warn("push notification API error",
			zap.Int("status_code", resp.StatusCode),
			zap.String("response", string(respBody)),
			zap.String("to", message.To))
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var tickets []ExpoTicket
	if err := json.Unmarshal(respBody, &tickets); err != nil {
		pns.logger.Error("failed to parse push notification response",
			zap.Error(err),
			zap.String("response", string(respBody)))
		return fmt.Errorf("parse error: %w", err)
	}

	// Check for errors in tickets
	for _, ticket := range tickets {
		if ticket.Error != "" {
			pns.logger.Warn("push notification error in ticket",
				zap.String("error", ticket.Error),
				zap.String("to", message.To))
			return fmt.Errorf("ticket error: %s", ticket.Error)
		}
	}

	pns.logger.Info("push notification sent successfully",
		zap.String("to", message.To),
		zap.String("title", message.Title))

	return nil
}

