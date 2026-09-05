package gateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
)

// What a deployment is allowed to do.
//
// A deployment used to run as whatever key somebody had pasted into its image,
// which is a namespace key: an application compromise was a namespace takeover.
// It is a principal of its own now, and this is where its owner says what it may
// reach. A deployment nobody has granted anything to reaches nothing, which is
// the only safe starting point.

// appGrantsHandler dispatches GET and POST /v1/deployments/grants.
func (g *Gateway) appGrantsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.listAppGrants(w, r)
	case http.MethodPost:
		g.setAppGrant(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (GET to list, POST to grant)")
	}
}

func (g *Gateway) listAppGrants(w http.ResponseWriter, r *http.Request) {
	ns := keysNamespace(r)
	if ns == "" {
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
		return
	}

	members, err := g.authService.ListMembers(r.Context(), ns)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	out := make([]map[string]any, 0)
	for _, m := range members {
		if m.PrincipalType != auth.PrincipalApp {
			continue
		}
		_, deployment, ok := auth.ParseWorkloadSubject(m.Identifier)
		if !ok || (name != "" && deployment != name) {
			continue
		}
		entry := map[string]any{
			"deployment": deployment,
			"role":       string(m.Role),
			"created_at": m.CreatedAt,
		}
		if m.Resource != "" {
			entry["resource"] = m.Resource
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"namespace": ns, "grants": out})
}

func (g *Gateway) setAppGrant(w http.ResponseWriter, r *http.Request) {
	ns := keysNamespace(r)
	if ns == "" {
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Name     string `json:"name"`
		Role     string `json:"role"`
		Resource string `json:"resource"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: expected JSON {name, role}")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required: which deployment is being granted")
		return
	}
	role, err := auth.ParseRole(body.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if role == auth.RoleAdmin {
		// An app with the control plane can deploy over itself, mint keys and
		// read the raw database. If that is what somebody wants they can say
		// so with a wallet; a deployment asking for it is almost always a
		// mistake, and it is the mistake this whole change exists to end.
		writeError(w, http.StatusBadRequest,
			"a deployment cannot hold the control plane: grant it 'runtime' for the data plane, "+
				"or name the grants it needs")
		return
	}

	if err := g.authService.EnsureWorkloadPrincipal(r.Context(), ns, body.Name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := g.authService.Grant(r.Context(), auth.GrantRequest{
		Namespace:     ns,
		PrincipalType: auth.PrincipalApp,
		Identifier:    auth.WorkloadSubject(ns, body.Name),
		DisplayName:   ns + "/" + body.Name,
		Role:          role,
		Resource:      strings.TrimSpace(body.Resource),
		CreatedBy:     auth.ActorFromRequest(r),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	g.authService.Audit().RecordFromRequest(r.Context(), r, auth.AuditEvent{
		Namespace: ns,
		Actor:     auth.ActorFromRequest(r),
		Action:    auth.AuditGrantAdded,
		Resource:  auth.WorkloadSubject(ns, body.Name),
		Result:    auth.AuditSuccess,
		Metadata:  map[string]string{"role": string(role)},
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"namespace":  ns,
		"deployment": body.Name,
		"role":       string(role),
		// The token is minted at start and renewed hourly, so a grant written
		// now reaches the running deployment on its next renewal rather than
		// immediately.
		"applies":  "on the deployment's next token renewal, or immediately on redeploy",
		"resource": body.Resource,
	})
}
