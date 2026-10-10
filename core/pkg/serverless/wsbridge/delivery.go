package wsbridge

import (
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
)

// DeliveryEventType is the `_orama` discriminator of a bridged message sent to
// a client that asked for platform-stamped delivery. It sits in the same
// reserved namespace as the platform's ephemeral-state events, which no publish
// route lets a caller send (see handlers/pubsub: PUBSUB_RESERVED_KEY).
const DeliveryEventType = "pubsub.message"

// Delivery is the frame a client that opted in receives for every bridged
// message, in place of the raw publisher bytes.
//
// The raw frame says nothing the platform vouches for: a publisher chooses
// every byte of it, including any `topic` field in it, so a client that
// routes on a field of the payload routes on what the publisher claims
// (bugboard #733). Here the topic is the one the platform delivered the message
// on, and the payload travels untouched and base64-encoded so that nothing in
// it can be read as part of the envelope.
type Delivery struct {
	Type    string `json:"_orama"`      // always DeliveryEventType
	Topic   string `json:"topic"`       // the topic the bridge delivered on
	DataB64 []byte `json:"data_base64"` // the publisher's bytes, base64 on the wire
}

// stampDelivery encodes the frame a client that opted in receives.
func stampDelivery(topic string, data []byte) ([]byte, error) {
	frame, err := json.Marshal(Delivery{Type: DeliveryEventType, Topic: topic, DataB64: data})
	if err != nil {
		return nil, fmt.Errorf("wsbridge: stamp delivery on topic %q: %w", topic, err)
	}
	return frame, nil
}

// SetClientDeliveryEnvelope marks a WS client as one that receives bridged
// messages as Delivery frames. It is called at upgrade, from the client's own
// opt-in, and cleared by RemoveClient. A client that never opted in keeps
// receiving the raw publisher bytes, as before.
func (b *Bridge) SetClientDeliveryEnvelope(clientID string) {
	b.mu.Lock()
	b.enveloped[clientID] = struct{}{}
	b.mu.Unlock()
}

// wantsEnvelope reports whether a client opted in to Delivery frames.
func (b *Bridge) wantsEnvelope(clientID string) bool {
	b.mu.RLock()
	_, ok := b.enveloped[clientID]
	b.mu.RUnlock()
	return ok
}

// forward fans an inbound libp2p message out to all bridged clients on the
// given (namespace, topic). Direct send; if a client's WS is slow/closed
// the send returns an error which we log-and-drop (no per-message buffering
// in v1; revisit if metrics show drops).
func (b *Bridge) forward(namespace, topic string, data []byte) {
	b.mu.RLock()
	tbl, ok := b.perNS[namespace]
	b.mu.RUnlock()
	if !ok {
		return
	}
	tbl.mu.Lock()
	clients := tbl.topicToClients[topic]
	cidSlice := make([]string, 0, len(clients))
	for c := range clients {
		cidSlice = append(cidSlice, c)
	}
	tbl.mu.Unlock()

	if b.ws == nil {
		return
	}
	// The stamped frame is built once and only if some client asked for it.
	var stamped []byte
	for _, cid := range cidSlice {
		payload := data
		if b.wantsEnvelope(cid) {
			if stamped == nil {
				var err error
				if stamped, err = stampDelivery(topic, data); err != nil {
					b.logger.Warn("wsbridge.forward: message not delivered to clients that asked for stamped delivery",
						zap.String("topic", topic), zap.Error(err))
					continue
				}
			}
			payload = stamped
		}
		if err := b.ws.Send(cid, payload); err != nil {
			b.logger.Debug("wsbridge.forward: ws send failed (slow/closed client)",
				zap.String("client_id", cid),
				zap.String("topic", topic),
				zap.Error(err))
		}
	}
}
