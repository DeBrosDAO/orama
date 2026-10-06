package gateway

import (
	"context"
	"net/http"
	"sync"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// Telling a client it is using a spelling that is going away.
//
// A credential is accepted six ways. Only `Authorization: Bearer <token>` (and
// the WebSocket query token, which a browser leaves no alternative to) is the
// one to keep. Removing the others would break every client at once, so the
// deprecated ones are answered as before and marked: on the response, so
// whoever is writing the client sees it, and once in the audit trail, so the
// namespace's owner can see who still has to move.

// deprecationNotice is the header a client gets back. Deprecation is RFC 8594's
// spelling; the second header says what to do about it, because "true" on its
// own is not actionable.
const (
	headerDeprecation      = "Deprecation"
	headerDeprecationAdvie = "X-Orama-Deprecation"
)

// credentialFormAdvice is what a client using each deprecated form should do.
var credentialFormAdvice = map[auth.KeyForm]string{
	auth.KeyFormHeader:       "this credential arrived in X-API-Key; send Authorization: Bearer <token> instead",
	auth.KeyFormApiKeyScheme: "this credential arrived as Authorization: ApiKey <token>; send Authorization: Bearer <token> instead",
	auth.KeyFormBare:         "this credential arrived as Authorization: <token> with no scheme; send Authorization: Bearer <token> instead",
}

// markDeprecatedCredential tells the client its credential arrived in a form
// that is going away. It is a no-op for the forms that are not.
func markDeprecatedCredential(w http.ResponseWriter, form auth.KeyForm) {
	advice, ok := credentialFormAdvice[form]
	if !ok {
		return
	}
	w.Header().Set(headerDeprecation, "true")
	w.Header().Set(headerDeprecationAdvie, advice)
}

// deprecationLog records each namespace's first use of each deprecated form.
//
// Once per pair, not once per request: the audit table is replicated to every
// node, and a row per request would fill it with a fact that is the same every
// time. What the trail is for here is "which of my clients still has to change",
// and one row answers that.
type deprecationLog struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newDeprecationLog() *deprecationLog {
	return &deprecationLog{seen: map[string]struct{}{}}
}

// firstUse reports whether this is the first time this gateway has seen this
// namespace use this form.
func (d *deprecationLog) firstUse(namespace string, form auth.KeyForm) bool {
	if d == nil {
		return false
	}
	key := namespace + "\x00" + string(form)
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[key]; ok {
		return false
	}
	d.seen[key] = struct{}{}
	return true
}

// recordDeprecatedCredential writes one audit event the first time a namespace
// uses a deprecated form on this gateway.
func (g *Gateway) recordDeprecatedCredential(r *http.Request, namespace string, form auth.KeyForm) {
	if !form.Deprecated() || !g.credentialDeprecations.firstUse(namespace, form) {
		return
	}
	g.authService.Audit().RecordFromRequest(context.WithoutCancel(r.Context()), r, auth.AuditEvent{
		Namespace: namespace,
		Actor:     auth.ActorFromRequest(r),
		Action:    auth.AuditLegacyCredential,
		Resource:  string(form),
		Result:    auth.AuditSuccess,
		Metadata:  map[string]string{"advice": credentialFormAdvice[form]},
	})
}
