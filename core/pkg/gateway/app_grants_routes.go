package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// appGrantApplies says when a grant written by setAppGrant takes effect. A
// workload's grant is read where its token is read, not copied into the token
// (#729): what was written decides the deployment's next request, on a node
// within the grant cache's ten seconds. The running app needs neither a new
// token nor a redeploy.
const appGrantApplies = "within seconds, without a redeploy or a new token: the grant is read wherever the deployment's token is checked (cached for 10 seconds on each node)"

// deploymentQuerier is the part of the cluster registry a grant needs to know a
// deployment exists. The registry client satisfies it.
type deploymentQuerier interface {
	Query(ctx context.Context, dest any, query string, args ...any) error
}

var (
	// errInvalidGrantName marks a name no deployment can have: a 400.
	errInvalidGrantName = errors.New("invalid deployment name")
	// errNoSuchDeployment marks a grant for a name that is no deployment of the
	// namespace: a 404. Such a grant would sit unused until a deployment of that
	// name appeared, and then apply to it.
	errNoSuchDeployment = errors.New("no such deployment")
)

// checkGrantTarget validates the deployment a grant names: the name has to be
// one a deployment can have (no '/' or ':', which would let the subject
// app:<namespace>/<name> read as another namespace's), and the deployment has
// to exist in this namespace.
func (g *Gateway) checkGrantTarget(ctx context.Context, namespace, name string) error {
	if err := process.ValidateInstance(namespace, name); err != nil {
		return fmt.Errorf("%w: %w", errInvalidGrantName, err)
	}
	if g.deploymentQuerier == nil {
		return fmt.Errorf("the deployment registry is not available on this gateway")
	}
	var rows []struct {
		ID string `db:"id"`
	}
	if err := g.deploymentQuerier.Query(ctx, &rows,
		"SELECT id FROM deployments WHERE namespace = ? AND name = ? LIMIT 1", namespace, name); err != nil {
		return fmt.Errorf("check that deployment %s/%s exists: %w", namespace, name, err)
	}
	if len(rows) == 0 {
		return fmt.Errorf("%s/%s: %w", namespace, name, errNoSuchDeployment)
	}
	return nil
}

// refuseGrantTarget answers a grant whose deployment could not be vouched for:
// a name no deployment can have is the caller's mistake, a missing deployment
// is a 404, and an unreadable registry is a retryable 503.
func (g *Gateway) refuseGrantTarget(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidGrantName):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errNoSuchDeployment):
		writeError(w, http.StatusNotFound, err.Error()+": deploy it first, then grant it")
	default:
		g.logger.ComponentError(logging.ComponentGeneral, "could not check the deployment a grant names", zap.Error(err))
		writeError(w, http.StatusServiceUnavailable, "the deployment could not be looked up right now; retry shortly")
	}
}

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
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required: which deployment is being granted")
		return
	}
	role, err := auth.ParseRole(body.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if role != auth.RoleRuntime && role != auth.RoleReader {
		// An app with the control plane (admin, owner, developer) can deploy
		// over itself, mint keys and read the raw database. If that is what
		// somebody wants they can say so with a wallet; a deployment asking
		// for it is almost always a mistake, and it is the mistake this whole
		// change exists to end.
		writeError(w, http.StatusBadRequest,
			"a deployment cannot hold the control plane: grant it 'runtime' for the data plane, "+
				"or 'reader' for none")
		return
	}

	if err := g.checkGrantTarget(r.Context(), ns, body.Name); err != nil {
		g.refuseGrantTarget(w, err)
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
		var invalid *auth.ErrInvalidGrant
		if errors.As(err, &invalid) {
			writeError(w, http.StatusBadRequest, invalid.Error())
			return
		}
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
		"applies":    appGrantApplies,
		"resource":   body.Resource,
	})
}
