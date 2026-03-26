package pubsub

import (
	"fmt"
	"net/http"
)

// PresenceHandler handles GET /v1/pubsub/presence?topic=mytopic
func (p *PubSubHandlers) PresenceHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ns := resolveNamespaceFromRequest(r)
	if ns == "" {
		writeError(w, http.StatusForbidden, "namespace not resolved")
		return
	}

	topic := r.URL.Query().Get("topic")
	if topic == "" {
		writeError(w, http.StatusBadRequest, "missing 'topic'")
		return
	}

	topicKey := fmt.Sprintf("%s.%s", ns, topic)

	p.presenceMu.RLock()
	members, ok := p.presenceMembers[topicKey]
	p.presenceMu.RUnlock()

	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"topic":   topic,
			"members": []PresenceMember{},
			"count":   0,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"topic":   topic,
		"members": members,
		"count":   len(members),
	})
}
