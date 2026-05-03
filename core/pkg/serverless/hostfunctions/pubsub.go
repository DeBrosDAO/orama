package hostfunctions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/pubsub"
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

// pubSubBatchEntry mirrors the JSON shape accepted by PubSubPublishBatch.
type pubSubBatchEntry struct {
	Topic   string `json:"topic"`
	DataB64 string `json:"data_base64"`
}

// PubSubPublishBatch publishes multiple messages in parallel.
//
// Input is JSON: [{"topic":"...","data_base64":"..."}, ...]
// Up to pubsub.MaxBatchSize entries per call.
//
// Default behavior is fail-fast (first publish error is returned). The
// host function does not currently expose a best-effort flag — WASM
// callers that need it should call this function multiple times in
// chunks they're willing to retry independently.
func (h *HostFunctions) PubSubPublishBatch(ctx context.Context, msgsJSON []byte) error {
	if h.pubsub == nil {
		return &serverless.HostFunctionError{Function: "pubsub_publish_batch", Cause: fmt.Errorf("pubsub not available")}
	}

	var entries []pubSubBatchEntry
	if err := json.Unmarshal(msgsJSON, &entries); err != nil {
		return &serverless.HostFunctionError{Function: "pubsub_publish_batch", Cause: fmt.Errorf("invalid json: %w", err)}
	}
	if len(entries) == 0 {
		return nil
	}
	if len(entries) > pubsub.MaxBatchSize {
		return &serverless.HostFunctionError{
			Function: "pubsub_publish_batch",
			Cause:    fmt.Errorf("too many messages: max %d per batch", pubsub.MaxBatchSize),
		}
	}

	msgs := make([]pubsub.TopicMessage, 0, len(entries))
	for i, e := range entries {
		if e.Topic == "" {
			return &serverless.HostFunctionError{
				Function: "pubsub_publish_batch",
				Cause:    fmt.Errorf("entry %d: empty topic", i),
			}
		}
		data, err := base64.StdEncoding.DecodeString(e.DataB64)
		if err != nil {
			return &serverless.HostFunctionError{
				Function: "pubsub_publish_batch",
				Cause:    fmt.Errorf("entry %d (topic %q): bad base64: %w", i, e.Topic, err),
			}
		}
		msgs = append(msgs, pubsub.TopicMessage{Topic: e.Topic, Data: data})
	}

	if err := h.pubsub.PublishBatch(ctx, msgs, pubsub.PublishBatchOptions{}); err != nil {
		return &serverless.HostFunctionError{Function: "pubsub_publish_batch", Cause: err}
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
