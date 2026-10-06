package pubsub

import (
	"net/http"
	"strings"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

const (
	// reservedTopicPrefix prefixes the topics the platform itself publishes on
	// (WebRTC membership, `_orama/webrtc/<room>`). The platform publishes to them
	// in-process, so no publish route has any business accepting one; and what
	// they carry is about other users, so a subscriber must hold a grant.
	reservedTopicPrefix = "_orama/"

	// CodeReservedTopic is the wire code of a request refused because it is about
	// a reserved topic.
	CodeReservedTopic = "PUBSUB_RESERVED_TOPIC"
)

// isReservedTopic reports whether topic is under the platform's prefix. The
// comparison ignores case so that a spelling that differs only in case cannot be
// the way round it.
func isReservedTopic(topic string) bool {
	return len(topic) >= len(reservedTopicPrefix) && strings.EqualFold(topic[:len(reservedTopicPrefix)], reservedTopicPrefix)
}

func writeReservedTopic(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message, "code": CodeReservedTopic})
}

// refuseReservedPublish answers a publish to a reserved topic, for every
// caller: the platform publishes these in-process, never through a route. It
// reports whether it refused.
func refuseReservedPublish(w http.ResponseWriter, topic string) bool {
	if !isReservedTopic(topic) {
		return false
	}
	writeReservedTopic(w, http.StatusForbidden,
		"topics under \""+reservedTopicPrefix+"\" are published by the platform only and cannot be published to")
	return true
}

// holdsTopicGrant reports whether the caller holds a grant on topic: a
// credential that can publish to it by grant, which a signed-in user with no
// grant in the namespace cannot (gwauth.NoGrantPermissions reads pub/sub and
// does not write it).
func holdsTopicGrant(r *http.Request, topic string) bool {
	return gwauth.AuthorizeResource(r.Context(), gwauth.Resource{
		Domain: gwauth.SelectorPubsub, Name: topic, Action: gwauth.ActionWrite,
	}) == nil
}

// refuseReservedSubscribe answers a subscription to a reserved topic by a
// caller that holds no grant. It reports whether it refused.
func refuseReservedSubscribe(w http.ResponseWriter, r *http.Request, topic string) bool {
	if !isReservedTopic(topic) || holdsTopicGrant(r, topic) {
		return false
	}
	writeReservedTopic(w, http.StatusForbidden,
		"topics under \""+reservedTopicPrefix+"\" carry platform events about other users; subscribing needs a grant on the namespace, which a signed-in user without one does not hold")
	return true
}

// withoutReservedTopics drops the reserved topics from a listing for a caller
// that may not subscribe to them.
func withoutReservedTopics(r *http.Request, topics []string) []string {
	kept := make([]string, 0, len(topics))
	for _, t := range topics {
		if !isReservedTopic(t) || holdsTopicGrant(r, t) {
			kept = append(kept, t)
		}
	}
	return kept
}
