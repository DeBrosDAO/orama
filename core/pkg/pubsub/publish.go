package pubsub

import (
	"context"
	"fmt"
	"time"
)

// Publish publishes a message to a topic
func (m *Manager) Publish(ctx context.Context, topic string, data []byte) error {
	if m.pubsub == nil {
		return fmt.Errorf("pubsub not initialized")
	}

	// Determine namespace (allow per-call override via context)
	ns := m.namespace
	if v := ctx.Value(CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ns = s
		}
	}

	namespacedTopic := fmt.Sprintf("%s.%s", ns, topic)

	// Get or create topic
	libp2pTopic, err := m.getOrCreateTopic(namespacedTopic)
	if err != nil {
		return fmt.Errorf("failed to get topic for publishing: %w", err)
	}

	// Wait briefly for mesh formation if no peers are in the mesh yet
	// GossipSub needs time to discover peers and form a mesh
	// With FloodPublish enabled, messages will be flooded to all connected peers
	// but we still want to give the mesh a chance to form for better delivery
	waitCtx, waitCancel := context.WithTimeout(ctx, 2*time.Second)
	defer waitCancel()

	// Check if we have peers in the mesh, wait up to 2 seconds for mesh formation
	meshFormed := false
	for i := 0; i < 20 && !meshFormed; i++ {
		peers := libp2pTopic.ListPeers()
		if len(peers) > 0 {
			meshFormed = true
			break // Mesh has formed, proceed with publish
		}
		select {
		case <-waitCtx.Done():
			meshFormed = true // Timeout, proceed anyway (FloodPublish will handle it)
		case <-time.After(100 * time.Millisecond):
			// Continue waiting
		}
	}

	// Publish message
	if err := libp2pTopic.Publish(ctx, data); err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}
