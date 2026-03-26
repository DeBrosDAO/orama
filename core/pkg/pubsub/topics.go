package pubsub

import (
	"fmt"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// getOrCreateTopic gets an existing topic or creates a new one
func (m *Manager) getOrCreateTopic(topicName string) (*pubsub.Topic, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Return existing topic if available
	if topic, exists := m.topics[topicName]; exists {
		return topic, nil
	}

	// Join the topic - LibP2P allows multiple clients to join the same topic
	topic, err := m.pubsub.Join(topicName)
	if err != nil {
		return nil, fmt.Errorf("failed to join topic: %w", err)
	}

	m.topics[topicName] = topic
	return topic, nil
}
