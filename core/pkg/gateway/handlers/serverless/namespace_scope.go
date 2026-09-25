package serverless

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
)

// Which namespace a request acts on.
//
// Bugboard #423: function management took the namespace from the request —
// deploy metadata, the `namespace` form field, ?namespace=, X-Namespace — and
// fell back to the credential's only when the request named none. The auth and
// ownership checks had been made against the credential's namespace, so a
// credential of namespace A could deploy into, delete from, or read the logs
// and secrets of namespace B just by naming it.
//
// Management now acts on the credential's namespace and nothing else. A
// request that names another one is refused rather than quietly redirected, so
// a caller who meant B learns that their credential is A's.

// headerNamespace is the header a client may name a namespace in.
const headerNamespace = "X-Namespace"

// credentialNamespace is the namespace the request's credential belongs to, as
// the auth middleware resolved it, or "" when the request carries none.
func credentialNamespace(r *http.Request) string {
	ns, _ := r.Context().Value(ctxkeys.NamespaceOverride).(string)
	return strings.TrimSpace(ns)
}

// managedNamespace resolves the namespace a management request acts on: the
// credential's. named are namespaces the request body names (deploy
// metadata, form fields); ?namespace= and X-Namespace are always checked. Any
// of them naming a different namespace is refused with 403.
//
// It reports whether the request may continue; on false the response has been
// written.
func managedNamespace(w http.ResponseWriter, r *http.Request, named ...string) (string, bool) {
	ns := credentialNamespace(r)
	if ns == "" {
		writeError(w, http.StatusForbidden,
			"the namespace this credential belongs to could not be resolved; functions are managed with a credential of their namespace")
		return "", false
	}
	named = append(named, r.URL.Query().Get("namespace"), r.Header.Get(headerNamespace))
	for _, other := range named {
		other = strings.TrimSpace(other)
		if other != "" && other != ns {
			writeError(w, http.StatusForbidden, fmt.Sprintf(
				"this credential belongs to namespace %q and cannot act on namespace %q; use a credential of %q",
				ns, other, other))
			return "", false
		}
	}
	return ns, true
}

// invokeNamespace is the namespace whose function an HTTP invocation runs:
// the one named with ?namespace=, else the credential's, else "" — an
// anonymous caller may run a public function, but has to say whose.
// POST /v1/invoke/<namespace>/<function> names it in the path instead.
func invokeNamespace(r *http.Request) string {
	if ns := strings.TrimSpace(r.URL.Query().Get("namespace")); ns != "" {
		return ns
	}
	return credentialNamespace(r)
}
