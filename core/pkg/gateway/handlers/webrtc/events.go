package webrtc

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"github.com/DeBrosOfficial/network/pkg/sfu/roomid"
	"go.uber.org/zap"
)

// Membership events: an SFU reports every join and leave to a namespace
// gateway (pkg/sfu/membership.go), which publishes it on the namespace's pubsub
// where functions and clients can subscribe (docs/WEBRTC.md#membership-events).

const (
	// EventTopicPrefix prefixes the topic a room's membership is published on.
	// The platform stamps every event with the reserved `_orama` envelope key,
	// which no publish route accepts from a caller, so a subscriber that finds
	// it knows the platform wrote the message and not an end user.
	EventTopicPrefix = "_orama/webrtc/"

	// maxEventBody bounds an event: a handful of ids.
	maxEventBody = 4096

	// eventPublishTimeout bounds the publish of one event.
	eventPublishTimeout = 5 * time.Second
)

// eventTypes maps what an SFU reports to the `_orama` discriminator published.
var eventTypes = map[string]string{
	ctrlauth.EventJoin:  "webrtc.join",
	ctrlauth.EventLeave: "webrtc.leave",
}

// membershipMessage is what subscribers receive.
type membershipMessage struct {
	Orama    string `json:"_orama"`
	Room     string `json:"room"`
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id,omitempty"`
	PeerID   string `json:"peer_id"`
	Reason   string `json:"reason,omitempty"`
	At       string `json:"at"`
}

// EventsHandler serves POST /v1/internal/webrtc/events, the SFU's report. The
// MAC is keyed by the namespace's TURN secret, so only an SFU of this namespace
// can report into it.
func (h *WebRTCHandlers) EventsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.controlKey == nil || h.publishEvent == nil {
		writeError(w, http.StatusServiceUnavailable, "this gateway cannot publish WebRTC membership events: no TURN secret or no pubsub")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEventBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "event body unreadable or too large")
		return
	}
	if err := h.verifyEvent(r, body); err != nil {
		writeError(w, http.StatusUnauthorized, "this route is reached by the namespace's SFUs, and the caller did not prove it is one: "+err.Error())
		return
	}
	msg, err := decodeEvent(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := json.Marshal(msg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode the membership event")
		return
	}
	ctx, cancel := contextWithTimeout(r, eventPublishTimeout)
	defer cancel()
	if err := h.publishEvent(ctx, EventTopicPrefix+msg.Room, data); err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "Failed to publish a WebRTC membership event",
			zap.String("room", msg.Room), zap.String("type", msg.Orama), zap.Error(err))
		writeError(w, http.StatusBadGateway, "failed to publish the membership event: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusOK)
}

// verifyEvent checks the report's MAC, then that it is not a copy of one
// already served.
func (h *WebRTCHandlers) verifyEvent(r *http.Request, body []byte) error {
	now, header := h.now(), r.Header.Get(ctrlauth.MACHeader)
	if err := ctrlauth.Verify(h.controlKey, h.eventSink, header, r.Method, r.URL.Path, body, now); err != nil {
		return err
	}
	return h.replays.Use(header, now)
}

// decodeEvent validates a reported event and shapes what is published.
func decodeEvent(body []byte) (membershipMessage, error) {
	var ev ctrlauth.MembershipEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return membershipMessage{}, errBadEvent("the body is not a membership event")
	}
	kind, ok := eventTypes[ev.Type]
	if !ok {
		return membershipMessage{}, errBadEvent("type must be join or leave")
	}
	if err := roomid.Validate(ev.Room); err != nil {
		return membershipMessage{}, errBadEvent("invalid room: " + err.Error())
	}
	if ev.UserID == "" || ev.PeerID == "" || ev.At.IsZero() {
		return membershipMessage{}, errBadEvent("user_id, peer_id and at are required")
	}
	return membershipMessage{
		Orama: kind, Room: ev.Room, UserID: ev.UserID, DeviceID: ev.DeviceID,
		PeerID: ev.PeerID, Reason: ev.Reason, At: ev.At.UTC().Format(time.RFC3339Nano),
	}, nil
}

type errBadEvent string

func (e errBadEvent) Error() string { return string(e) }
