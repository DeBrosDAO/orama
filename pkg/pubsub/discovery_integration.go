package pubsub

import (
	"context"
	"log"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// announceTopicInterest helps with peer discovery by announcing interest in a topic.
// It starts lightweight monitoring and performs a single proactive announcement to
// encourage peers to respond. This implementation is application-agnostic.
func (m *Manager) announceTopicInterest(topicName string) {
	// Wait a bit for the subscription to be established
	time.Sleep(100 * time.Millisecond)

	// Get the topic
	m.mu.RLock()
	topic, exists := m.topics[topicName]
	m.mu.RUnlock()

	if !exists {
		return
	}

	// Start periodic monitoring for topic peers
	go m.monitorTopicPeers(topicName, topic)

	// Perform a single proactive announcement to the topic to encourage peers to respond
	go m.forceTopicPeerDiscovery(topicName, topic)
}

// forceTopicPeerDiscovery uses a simple strategy to announce presence on the topic.
// It publishes lightweight discovery pings continuously to maintain mesh health.
func (m *Manager) forceTopicPeerDiscovery(topicName string, topic *pubsub.Topic) {
	log.Printf("[PUBSUB] Starting continuous peer discovery for topic: %s", topicName)
	
	// Initial aggressive discovery phase (10 attempts)
	for attempt := 0; attempt < 10; attempt++ {
		peers := topic.ListPeers()
		if len(peers) > 0 {
			log.Printf("[PUBSUB] Topic %s: Found %d peers in initial discovery", topicName, len(peers))
			break
		}

		log.Printf("[PUBSUB] Topic %s: Initial attempt %d, sending discovery ping", topicName, attempt+1)
		
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		discoveryMsg := []byte("PEER_DISCOVERY_PING")
		_ = topic.Publish(ctx, discoveryMsg)
		cancel()

		delay := time.Duration(100*(attempt+1)) * time.Millisecond
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		time.Sleep(delay)
	}
	
	// Continuous maintenance phase - keep pinging every 15 seconds
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	
	for i := 0; i < 20; i++ { // Run for ~5 minutes total
		<-ticker.C
		peers := topic.ListPeers()
		
		if len(peers) == 0 {
			log.Printf("[PUBSUB] Topic %s: No peers, sending maintenance ping", topicName)
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			discoveryMsg := []byte("PEER_DISCOVERY_PING")
			_ = topic.Publish(ctx, discoveryMsg)
			cancel()
		}
	}
	
	log.Printf("[PUBSUB] Topic %s: Peer discovery maintenance completed", topicName)
}

// monitorTopicPeers periodically checks topic peer connectivity and stops once peers are found.
func (m *Manager) monitorTopicPeers(topicName string, topic *pubsub.Topic) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for i := 0; i < 6; i++ { // Monitor for ~30 seconds
		<-ticker.C
		peers := topic.ListPeers()

		// If we have peers, stop monitoring
		if len(peers) > 0 {
			return
		}
	}
}
