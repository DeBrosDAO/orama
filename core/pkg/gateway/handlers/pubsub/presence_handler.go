package pubsub

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
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

// presenceSession is one socket's presence membership; the zero value is a
// socket without presence.
type presenceSession struct {
	ns, topic, topicKey string
	memberID, connID    string
	enabled             bool
}

// joinPresence lists the member and broadcasts presence.join. It does nothing
// for a socket that did not ask for presence.
func (p *PubSubHandlers) joinPresence(ns, topic string, enabled bool, memberID string, meta map[string]interface{}) presenceSession {
	if !enabled {
		return presenceSession{}
	}
	sess := presenceSession{
		ns: ns, topic: topic, topicKey: fmt.Sprintf("%s.%s", ns, topic),
		memberID: memberID, connID: uuid.New().String(), enabled: true,
	}
	member := PresenceMember{
		MemberID: memberID,
		JoinedAt: time.Now().Unix(),
		Meta:     meta,
		ConnID:   sess.connID,
	}
	p.presenceMu.Lock()
	p.presenceMembers[sess.topicKey] = append(p.presenceMembers[sess.topicKey], member)
	p.presenceMu.Unlock()

	// Every subscriber, this one included, receives the join through the pubsub service.
	p.broadcastPresenceEvent(ns, topic, "presence.join", memberID, meta, member.JoinedAt)
	p.logger.ComponentInfo("gateway", "pubsub ws: member joined presence",
		zap.String("topic", topic),
		zap.String("member_id", memberID))
	return sess
}

// leavePresence unlists the member and broadcasts presence.leave.
func (p *PubSubHandlers) leavePresence(sess presenceSession) {
	if !sess.enabled {
		return
	}
	p.presenceMu.Lock()
	members := p.presenceMembers[sess.topicKey]
	for i, m := range members {
		if m.ConnID == sess.connID {
			p.presenceMembers[sess.topicKey] = append(members[:i], members[i+1:]...)
			break
		}
	}
	if len(p.presenceMembers[sess.topicKey]) == 0 {
		delete(p.presenceMembers, sess.topicKey)
	}
	p.presenceMu.Unlock()

	p.broadcastPresenceEvent(sess.ns, sess.topic, "presence.leave", sess.memberID, nil, time.Now().Unix())
	p.logger.ComponentInfo("gateway", "pubsub ws: member left presence",
		zap.String("topic", sess.topic),
		zap.String("member_id", sess.memberID))
}
