package push

// topics.go — push registrations addressed by a rotating topic (FEAT-265).
//
//	POST   /v1/push/topics       — register or refresh a topic ({topic_secret, provider, token})
//	DELETE /v1/push/topics       — remove a topic ({topic_secret})
//	POST   /v1/push/topics/send  — send to a topic (same grant as /v1/push/send)
//
// The register and remove handlers never read the caller's identity: the
// route's push grant decides who may call them, and possession of the topic
// secret decides which topic a call acts on. Nothing about the caller is
// stored with the topic or logged beside it, and the topic id is not logged.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/push"
	"go.uber.org/zap"
)

// RegisterTopicRequest is the body of POST /v1/push/topics.
//
// TopicSecret is the device's hex-encoded secret (16–64 random bytes). The
// topic id is its SHA-256; the secret itself is never stored.
type RegisterTopicRequest struct {
	TopicSecret string `json:"topic_secret"`
	Provider    string `json:"provider"` // "ntfy" | "expo" | "apns" | "apns_voip"
	Token       string `json:"token"`
}

// RegisterTopicResponse is the body of a successful POST /v1/push/topics.
// ExpiresAt (unix seconds) is when the registration lapses unless refreshed.
type RegisterTopicResponse struct {
	Status    string `json:"status"`
	TopicID   string `json:"topic_id"`
	ExpiresAt int64  `json:"expires_at"`
}

// UnregisterTopicRequest is the body of DELETE /v1/push/topics.
type UnregisterTopicRequest struct {
	TopicSecret string `json:"topic_secret"`
}

// SendTopicRequest is the body of POST /v1/push/topics/send.
type SendTopicRequest struct {
	TopicID string `json:"topic_id"`
	PushContent
}

// topicStore is the topic store behind the per-namespace Manager, or nil when
// push is not configured on this gateway.
func (h *Handlers) topicStore() push.PushTopicStore {
	if h.manager == nil {
		return nil
	}
	return h.manager.TopicStore()
}

// RegisterTopicHandler handles POST /v1/push/topics. Re-registering with the
// same secret refreshes the expiry and may re-point the topic to a new token;
// a token already registered under another topic moves to this one.
func (h *Handlers) RegisterTopicHandler(w http.ResponseWriter, r *http.Request) {
	topics := h.topicStore()
	if topics == nil {
		writeError(w, http.StatusServiceUnavailable, push.ErrTopicsNotConfigured.Error())
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ns := resolveNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBodyBytes)
	var body RegisterTopicRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	topicID, err := push.TopicIDFromSecret(strings.TrimSpace(body.TopicSecret))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	provider, token := strings.TrimSpace(body.Provider), strings.TrimSpace(body.Token)
	if err := validateProviderToken(provider, token); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	expiresAt, err := topics.Register(boundCtx(r), push.PushTopic{
		Namespace: ns, TopicID: topicID, Provider: provider, Token: token,
	})
	if err != nil {
		h.logger.ComponentWarn("push", "topic registration failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "registration failed")
		return
	}
	writeJSON(w, http.StatusOK, RegisterTopicResponse{Status: "ok", TopicID: topicID, ExpiresAt: expiresAt})
}

// UnregisterTopicHandler handles DELETE /v1/push/topics. Only the holder of
// the secret can name the topic, so a wrong secret is simply a topic that is
// not there: 404.
func (h *Handlers) UnregisterTopicHandler(w http.ResponseWriter, r *http.Request) {
	topics := h.topicStore()
	if topics == nil {
		writeError(w, http.StatusServiceUnavailable, push.ErrTopicsNotConfigured.Error())
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ns := resolveNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBodyBytes)
	var body UnregisterTopicRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	topicID, err := push.TopicIDFromSecret(strings.TrimSpace(body.TopicSecret))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	err = topics.Unregister(boundCtx(r), ns, topicID)
	if errors.Is(err, push.ErrTopicNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		h.logger.ComponentWarn("push", "topic removal failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "unregister failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// SendTopicHandler handles POST /v1/push/topics/send — the HTTP equivalent of
// the push_send_topic host function. Who may call it is decided by the route
// policy alone, which gives it exactly /v1/push/send's grant (push:write with
// ownership); the handler reads no caller identity.
func (h *Handlers) SendTopicHandler(w http.ResponseWriter, r *http.Request) {
	if h.topicStore() == nil {
		writeError(w, http.StatusServiceUnavailable, push.ErrTopicsNotConfigured.Error())
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ns := resolveNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSendBodyBytes)
	var body SendTopicRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	topicID := strings.TrimSpace(body.TopicID)
	if err := push.ValidateTopicID(topicID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	res, err := h.manager.SendToTopicDetailed(boundCtx(r), ns, topicID, body.message())
	h.writeTopicSendOutcome(w, ns, res, err)
}

// writeTopicSendOutcome maps a topic send's outcome to the HTTP response.
func (h *Handlers) writeTopicSendOutcome(w http.ResponseWriter, ns string, res *push.SendDetailedResult, err error) {
	switch {
	case errors.Is(err, push.ErrPushNotConfigured):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, push.ErrTopicNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		h.logger.ComponentWarn("push", "topic send failed",
			zap.String("namespace", ns), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "send failed")
	case !res.Ok:
		// The dispatcher has already logged the provider's status and reason.
		writeError(w, http.StatusBadGateway, "delivery to the topic's device failed")
	default:
		writeJSON(w, http.StatusOK, SendResponse{Status: "ok"})
	}
}
