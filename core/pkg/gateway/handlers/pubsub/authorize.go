package pubsub

import (
	"net/http"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// A grant may be narrowed to a topic pattern — `pubsub:topic=chat.*` — and this
// is where that is applied. A credential with no selector is unaffected: the
// scope gate has already decided whether it may reach pub/sub at all, and this
// only ever takes access away.
//
// It is what lets a tenant isolate its own end users. Before it, a `pubsub`
// grant was every topic in the namespace, so one leaked runtime key read every
// conversation in the application.
func authorizeTopic(w http.ResponseWriter, r *http.Request, topic string, action gwauth.Action) bool {
	err := gwauth.AuthorizeResource(r.Context(), gwauth.Resource{
		Domain: gwauth.SelectorPubsub,
		Name:   topic,
		Action: action,
	})
	if err == nil {
		return true
	}
	writeError(w, http.StatusForbidden, err.Error())
	return false
}
