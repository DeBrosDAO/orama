package hostfunctions

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/serverless"
)

// PubSubPublish publishes a message to a topic.
func (h *HostFunctions) PubSubPublish(ctx context.Context, topic string, data []byte) error {
	if h.pubsub == nil {
		return &serverless.HostFunctionError{Function: "pubsub_publish", Cause: fmt.Errorf("pubsub not available")}
	}

	// The pubsub adapter handles namespacing internally
	if err := h.pubsub.Publish(ctx, topic, data); err != nil {
		return &serverless.HostFunctionError{Function: "pubsub_publish", Cause: err}
	}

	return nil
}

// WSSend sends data to a specific WebSocket client.
func (h *HostFunctions) WSSend(ctx context.Context, clientID string, data []byte) error {
	if h.wsManager == nil {
		return &serverless.HostFunctionError{Function: "ws_send", Cause: serverless.ErrWSNotAvailable}
	}

	// If no clientID provided, use the current invocation's client
	if clientID == "" {
		h.invCtxLock.RLock()
		if h.invCtx != nil && h.invCtx.WSClientID != "" {
			clientID = h.invCtx.WSClientID
		}
		h.invCtxLock.RUnlock()
	}

	if clientID == "" {
		return &serverless.HostFunctionError{Function: "ws_send", Cause: serverless.ErrWSNotAvailable}
	}

	if err := h.wsManager.Send(clientID, data); err != nil {
		return &serverless.HostFunctionError{Function: "ws_send", Cause: err}
	}

	return nil
}

// WSBroadcast sends data to all WebSocket clients subscribed to a topic.
func (h *HostFunctions) WSBroadcast(ctx context.Context, topic string, data []byte) error {
	if h.wsManager == nil {
		return &serverless.HostFunctionError{Function: "ws_broadcast", Cause: serverless.ErrWSNotAvailable}
	}

	if err := h.wsManager.Broadcast(topic, data); err != nil {
		return &serverless.HostFunctionError{Function: "ws_broadcast", Cause: err}
	}

	return nil
}
