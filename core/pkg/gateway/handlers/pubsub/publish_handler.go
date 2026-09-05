package pubsub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
	"go.uber.org/zap"
)

// MaxPublishBatchSize is the maximum number of messages allowed in a single
// /v1/pubsub/publish-batch request. Mirrors pubsub.MaxBatchSize.
const MaxPublishBatchSize = pubsub.MaxBatchSize

// PublishHandler handles POST /v1/pubsub/publish {topic, data_base64}
func (p *PubSubHandlers) PublishHandler(w http.ResponseWriter, r *http.Request) {
	if p.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var body PublishRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Topic == "" || body.DataB64 == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {topic,data_base64}")
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.DataB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base64 data")
		return
	}

	if !authorizeTopic(w, r, body.Topic, gwauth.ActionWrite) {
		return
	}

	p.deliverLocal(ns, body.Topic, data)

	// Publish to libp2p asynchronously for cross-node delivery.
	go func() {
		publishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ctx := pubsub.WithNamespace(client.WithInternalAuth(publishCtx), ns)
		if err := p.client.PubSub().Publish(ctx, body.Topic, data); err != nil {
			p.logger.ComponentWarn("gateway", "async libp2p publish failed",
				zap.String("topic", body.Topic),
				zap.Error(err))
		}
	}()

	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// PublishBatchRequest is the request body for POST /v1/pubsub/publish-batch.
type PublishBatchRequest struct {
	Messages   []PublishBatchEntry `json:"messages"`
	BestEffort bool                `json:"best_effort,omitempty"`
}

// PublishBatchEntry is one message in a batch publish request.
type PublishBatchEntry struct {
	Topic   string `json:"topic"`
	DataB64 string `json:"data_base64"`
}

// PublishBatchResponse is the response body for /v1/pubsub/publish-batch.
//
// libp2p delivery is asynchronous and not awaited here, mirroring the
// single-publish handler's fire-and-forget contract. Per-topic failures
// are not surfaced via this response — operators should consult logs /
// metrics for delivery health.
type PublishBatchResponse struct {
	Status string `json:"status"` // always "ok" — request was accepted
}

// MaxPerMessageBytes caps an individual message payload inside a batch.
// Mirrors the 1MB cap on /v1/pubsub/publish.
const MaxPerMessageBytes = 1 << 20

// PublishBatchHandler handles POST /v1/pubsub/publish-batch.
// Accepts up to MaxPublishBatchSize messages and publishes them in parallel,
// preserving namespace isolation. Local subscribers receive messages
// immediately; libp2p delivery is async.
func (p *PubSubHandlers) PublishBatchHandler(w http.ResponseWriter, r *http.Request) {
	if p.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	// Limit body size: MaxPublishBatchSize messages * ~1MB each = up to ~100MB.
	// Cap conservatively at 16MB to discourage huge payloads.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)

	var body PublishBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: expected {messages:[{topic,data_base64}]}")
		return
	}
	if len(body.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages required")
		return
	}
	if len(body.Messages) > MaxPublishBatchSize {
		writeError(w, http.StatusBadRequest, "too many messages: max is 100 per batch")
		return
	}

	// Decode all messages up-front so we can fail fast on bad input.
	decoded := make([]pubsub.TopicMessage, 0, len(body.Messages))
	for i, m := range body.Messages {
		if m.Topic == "" {
			writeError(w, http.StatusBadRequest, "message missing topic at index "+strconv.Itoa(i))
			return
		}
		data, err := base64.StdEncoding.DecodeString(m.DataB64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid base64 data at index "+strconv.Itoa(i))
			return
		}
		if len(data) > MaxPerMessageBytes {
			writeError(w, http.StatusBadRequest, "message too large at index "+strconv.Itoa(i))
			return
		}
		// Every topic in the batch, before any of them is delivered: a batch
		// that is half refused is worse than one that is refused.
		if !authorizeTopic(w, r, m.Topic, gwauth.ActionWrite) {
			return
		}
		decoded = append(decoded, pubsub.TopicMessage{Topic: m.Topic, Data: data})
	}

	// Deliver locally + dispatch triggers per topic synchronously (fast in-process).
	for _, msg := range decoded {
		p.deliverLocal(ns, msg.Topic, msg.Data)
	}

	// Async libp2p batch publish, similar to PublishHandler's approach.
	go func() {
		publishCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ctx := pubsub.WithNamespace(client.WithInternalAuth(publishCtx), ns)
		opts := pubsub.PublishBatchOptions{BestEffort: body.BestEffort}
		err := p.client.PubSub().PublishBatch(ctx, toClientMessages(decoded), clientOpts(opts))
		if err != nil {
			p.logger.ComponentWarn("gateway", "async libp2p batch publish failed",
				zap.Int("messages", len(decoded)),
				zap.Error(err))
		}
	}()

	writeJSON(w, http.StatusOK, PublishBatchResponse{Status: "ok"})
}

// deliverLocal handles local-subscriber delivery and fires PubSub triggers.
// It does NOT publish to libp2p — callers handle that themselves (single
// or batched) so this helper stays focused on in-process fan-out.
func (p *PubSubHandlers) deliverLocal(ns, topic string, data []byte) {
	p.mu.RLock()
	localSubs := p.getLocalSubscribers(topic, ns)
	p.mu.RUnlock()

	localDeliveryCount := 0
	if len(localSubs) > 0 {
		for _, sub := range localSubs {
			select {
			case sub.msgChan <- data:
				localDeliveryCount++
			default:
				p.logger.ComponentWarn("gateway", "local subscriber buffer full, dropping message",
					zap.String("topic", topic))
			}
		}
	}

	p.logger.ComponentInfo("gateway", "pubsub publish: processing message",
		zap.String("topic", topic),
		zap.String("namespace", ns),
		zap.Int("data_len", len(data)),
		zap.Int("local_subscribers", len(localSubs)),
		zap.Int("local_delivered", localDeliveryCount))

	// Fire PubSub triggers for serverless functions (non-blocking).
	if p.onPublish != nil {
		go p.onPublish(context.Background(), ns, topic, data)
	}
}

// toClientMessages converts pubsub.TopicMessage to client.TopicMessage for
// passing through the PubSubClient interface.
func toClientMessages(msgs []pubsub.TopicMessage) []client.TopicMessage {
	out := make([]client.TopicMessage, len(msgs))
	for i, m := range msgs {
		out[i] = client.TopicMessage{Topic: m.Topic, Data: m.Data}
	}
	return out
}

func clientOpts(o pubsub.PublishBatchOptions) client.PublishBatchOptions {
	return client.PublishBatchOptions{BestEffort: o.BestEffort, MaxConcurrency: o.MaxConcurrency}
}

// TopicsHandler lists topics within the caller's namespace
func (p *PubSubHandlers) TopicsHandler(w http.ResponseWriter, r *http.Request) {
	if p.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	// Apply namespace isolation
	ctx := pubsub.WithNamespace(client.WithInternalAuth(r.Context()), ns)
	all, err := p.client.PubSub().ListTopics(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Client returns topics already trimmed to its namespace; return as-is
	writeJSON(w, http.StatusOK, map[string]any{"topics": all})
}

// writeError writes an error response
func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}
