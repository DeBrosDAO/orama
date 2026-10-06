package push

// topic.go — push registrations addressed by a rotating topic (FEAT-265).
//
// The account path (PushDevice) keys a device on the caller's identity, so the
// gateway holds a lasting account → device mapping. A topic registration keys
// it on a value the device chooses instead: the device generates a random
// secret, the topic id is the SHA-256 of that secret, and the gateway stores
// only the topic id. Senders are given the topic id; registering, refreshing
// and removing the topic require the secret, so a contact who knows a topic id
// can push to it but cannot re-point or delete it.
//
// Nothing here records which account a topic belongs to. What the gateway can
// still link is described in docs/PUSH_NOTIFICATIONS.md: the provider token is
// itself a stable device identifier.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	// TopicTTL is the least time a topic registration lives after it was last
	// registered. Re-registering with the same secret moves the expiry
	// forward; a device that stops refreshing stops receiving.
	TopicTTL = 7 * 24 * time.Hour

	// TopicExpiryGranularity is what a stored expiry is rounded UP to, so a
	// registration lives between TopicTTL and TopicTTL plus this. The stored
	// time must not pin down when a device registered: an exact second could
	// be joined against request logs to find the caller.
	TopicExpiryGranularity = 24 * time.Hour

	// TopicSecretMinBytes is the smallest secret accepted: 128 bits, so a
	// topic id cannot be registered by guessing its secret.
	TopicSecretMinBytes = 16

	// TopicSecretMaxBytes bounds the secret so the request stays small.
	TopicSecretMaxBytes = 64

	// topicIDHexLen is the length of a hex-encoded SHA-256 digest.
	topicIDHexLen = sha256.Size * 2
)

var (
	// ErrTopicNotFound is returned when a topic is not registered in the
	// namespace, or its registration has expired.
	ErrTopicNotFound = errors.New("push: topic not registered or expired")

	// ErrInvalidTopicSecret is returned for a secret that is not hex, or does
	// not decode to TopicSecretMinBytes..TopicSecretMaxBytes bytes.
	ErrInvalidTopicSecret = errors.New("push: invalid topic secret")

	// ErrInvalidTopicID is returned for a topic id that is not 64 lowercase
	// hex characters.
	ErrInvalidTopicID = errors.New("push: invalid topic id")

	// ErrTopicsNotConfigured is returned by a Manager that was never given a
	// topic store, and is what the HTTP handlers answer when push is off.
	ErrTopicsNotConfigured = errors.New("push: topic store not configured")
)

// PushTopic is a push registration addressed by topic id. Token is plaintext
// in this struct; the store encrypts it. There is deliberately no user field.
type PushTopic struct {
	Namespace string
	TopicID   string // lowercase hex SHA-256 of the device's secret
	Provider  string // matches PushProvider.Name()
	Token     string
	ExpiresAt int64 // unix seconds, a multiple of TopicExpiryGranularity
}

// PushTopicStore persists topic registrations.
type PushTopicStore interface {
	// Register creates or refreshes the topic and returns its new expiry
	// (unix seconds). The provider token belongs to one topic per namespace:
	// registering it under a new topic removes the topic it was under, in the
	// same atomic write.
	Register(ctx context.Context, topic PushTopic) (int64, error)

	// Unregister removes the topic. ErrTopicNotFound when there is none.
	Unregister(ctx context.Context, namespace, topicID string) error

	// Get returns the live (unexpired) topic with its token decrypted.
	// ErrTopicNotFound when it is absent or expired.
	Get(ctx context.Context, namespace, topicID string) (*PushTopic, error)
}

// TopicIDFromSecret validates a hex-encoded topic secret and returns its topic
// id, the lowercase hex SHA-256 of the decoded bytes. The error never contains
// the secret.
func TopicIDFromSecret(secret string) (string, error) {
	if len(secret) < TopicSecretMinBytes*2 || len(secret) > TopicSecretMaxBytes*2 {
		return "", fmt.Errorf("%w: must be %d to %d bytes, hex-encoded",
			ErrInvalidTopicSecret, TopicSecretMinBytes, TopicSecretMaxBytes)
	}
	raw, err := hex.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("%w: must be hex-encoded", ErrInvalidTopicSecret)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateTopicID checks that id is a lowercase hex SHA-256 digest, the only
// form TopicIDFromSecret produces.
func ValidateTopicID(id string) error {
	if len(id) != topicIDHexLen {
		return fmt.Errorf("%w: must be %d lowercase hex characters", ErrInvalidTopicID, topicIDHexLen)
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("%w: must be %d lowercase hex characters", ErrInvalidTopicID, topicIDHexLen)
		}
	}
	return nil
}

// SetTopicStore gives the manager its topic store. Call it once, before the
// manager serves any request.
func (m *Manager) SetTopicStore(s PushTopicStore) {
	m.topics = s
}

// TopicStore exposes the topic store to the HTTP handlers that register and
// remove topics. Nil when none was set.
func (m *Manager) TopicStore() PushTopicStore {
	return m.topics
}

// SendToTopicDetailed delivers msg to the device registered under topicID in
// the namespace, through the same provider dispatch as SendToUserDetailed.
//
// Errors: ErrPushNotConfigured when the namespace has no provider,
// ErrTopicNotFound when the topic is not registered or has expired,
// ErrTopicsNotConfigured when no topic store was set. Per-device delivery
// failures are in the result, as for SendToUserDetailed.
func (m *Manager) SendToTopicDetailed(ctx context.Context, namespace, topicID string, msg PushMessage) (*SendDetailedResult, error) {
	if m.topics == nil {
		return nil, ErrTopicsNotConfigured
	}
	d, err := m.dispatcherFor(ctx, namespace)
	if err != nil {
		return nil, err
	}
	topic, err := m.topics.Get(ctx, namespace, topicID)
	if err != nil {
		return nil, err
	}
	dev := PushDevice{Namespace: namespace, Provider: topic.Provider, Token: topic.Token}
	return d.sendToDevicesDetailed(ctx, []PushDevice{dev}, msg), nil
}
