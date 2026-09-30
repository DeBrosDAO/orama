package pubsub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

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

	// Hand the message to the pubsub service inside the request. The service
	// delivers it, in the order it receives it, to every subscriber on this
	// node and on others; answering only once it has it keeps one publisher's
	// messages in order and tells the publisher when one did not get through.
	ctx, cancel := context.WithTimeout(r.Context(), p.publishTimeout)
	defer cancel()
	ctx = pubsub.WithNamespace(client.WithInternalAuth(ctx), ns)
	if err := p.client.PubSub().Publish(ctx, body.Topic, data); err != nil {
		p.writePublishError(w, fmt.Sprintf("topic %q", body.Topic), err)
		return
	}
	p.firePublishTriggers(ns, body.Topic, data)

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
// It is sent only once the pubsub service holds every message; a batch it does
// not accept is answered with an error status and an {"error": ...} body.
type PublishBatchResponse struct {
	Status string `json:"status"` // always "ok" when this body is sent
}

// MaxPerMessageBytes caps an individual message payload inside a batch.
// Mirrors the 1MB cap on /v1/pubsub/publish.
const MaxPerMessageBytes = 1 << 20

// PublishBatchHandler handles POST /v1/pubsub/publish-batch.
// Accepts up to MaxPublishBatchSize messages and publishes them in parallel,
// preserving namespace isolation. It answers once the pubsub service, which
// delivers to subscribers on this node and on others, has accepted the batch.
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

	ctx, cancel := context.WithTimeout(r.Context(), p.publishTimeout)
	defer cancel()
	ctx = pubsub.WithNamespace(client.WithInternalAuth(ctx), ns)
	opts := client.PublishBatchOptions{BestEffort: body.BestEffort}
	if err := p.client.PubSub().PublishBatch(ctx, toClientMessages(decoded), opts); err != nil {
		p.writePublishError(w, fmt.Sprintf("a batch of %d messages", len(decoded)), err)
		return
	}
	for _, msg := range decoded {
		p.firePublishTriggers(ns, msg.Topic, msg.Data)
	}

	writeJSON(w, http.StatusOK, PublishBatchResponse{Status: "ok"})
}

// writePublishError answers a publish the pubsub service did not accept: 504
// when it did not answer in time, 503 otherwise. what names the message(s).
func (p *PubSubHandlers) writePublishError(w http.ResponseWriter, what string, err error) {
	p.logger.ComponentWarn("gateway", "pubsub publish failed",
		zap.String("what", what), zap.Error(err))
	if errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, fmt.Sprintf(
			"publish of %s: the pubsub service did not answer within %s, check that orama-namespace-pubsub@index is running: %v",
			what, p.publishTimeout, err))
		return
	}
	writeError(w, http.StatusServiceUnavailable, fmt.Sprintf(
		"publish of %s was not handed to the pubsub service, check that orama-namespace-pubsub@index is running: %v",
		what, err))
}

// firePublishTriggers dispatches the PubSub triggers of serverless functions
// for a message published through this gateway. It does not deliver the message
// to anyone: subscribers, on this node or another, receive it from the node's
// pubsub service, which the caller publishes to.
func (p *PubSubHandlers) firePublishTriggers(ns, topic string, data []byte) {
	p.logger.ComponentInfo("gateway", "pubsub publish: processing message",
		zap.String("topic", topic),
		zap.String("namespace", ns),
		zap.Int("data_len", len(data)))

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
