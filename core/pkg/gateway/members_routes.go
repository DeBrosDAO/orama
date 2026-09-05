package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"go.uber.org/zap"
)

// Who else may work in this namespace.
//
// There was no answer to that question and no way to change it: a namespace had
// one owner and everybody else was refused, so a second person on a team was
// given the owner's credentials or nothing. These endpoints are the roles made
// operable — list who is here, add somebody with less than everything, take it
// back, hand the namespace over.
//
// They are admin-scoped and namespace-owned, so a runtime key out of an
// application bundle cannot reach them. Transferring goes further and requires
// the owner: an admin who could transfer could take the namespace.

// maxGrantHours bounds an expiring grant. A year is not a temporary grant, and
// an unbounded number would let one row hold a value the column cannot compare.
const maxGrantHours = 24 * 365

func (g *Gateway) namespaceMembersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		g.listNamespaceMembers(w, r)
	case http.MethodPost:
		g.addNamespaceMember(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (GET to list, POST to add)")
	}
}

// namespaceMemberByIDHandler dispatches DELETE /v1/namespace/members/{wallet}
// and POST /v1/namespace/members/transfer.
func (g *Gateway) namespaceMemberByIDHandler(w http.ResponseWriter, r *http.Request) {
	sub := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/namespace/members/"), "/")

	if sub == "transfer" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed (POST)")
			return
		}
		g.transferNamespace(w, r)
		return
	}

	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed (DELETE /{wallet})")
		return
	}
	g.removeNamespaceMember(w, r, sub)
}

func (g *Gateway) listNamespaceMembers(w http.ResponseWriter, r *http.Request) {
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

	out := make([]map[string]any, 0, len(members))
	for _, m := range members {
		entry := map[string]any{
			"type":       string(m.PrincipalType),
			"identifier": m.Identifier,
			"role":       string(m.Role),
			"created_at": m.CreatedAt,
		}
		if m.DisplayName != "" {
			entry["display_name"] = m.DisplayName
		}
		if m.CreatedBy != "" {
			entry["created_by"] = m.CreatedBy
		}
		if m.ExpiresAt != "" {
			entry["expires_at"] = m.ExpiresAt
		}
		if m.Resource != "" {
			entry["resource"] = m.Resource
			// Said on every row that has one, so nobody reads a narrowed grant
			// as a working one.
			entry["enforced"] = false
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"namespace": ns, "members": out})
}

func (g *Gateway) addNamespaceMember(w http.ResponseWriter, r *http.Request) {
	ns := keysNamespace(r)
	if ns == "" {
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Wallet         string `json:"wallet"`
		Role           string `json:"role"`
		Resource       string `json:"resource"`
		DisplayName    string `json:"display_name"`
		ExpiresInHours int    `json:"expires_in_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: expected JSON {wallet, role}")
		return
	}
	if strings.TrimSpace(body.Wallet) == "" {
		writeError(w, http.StatusBadRequest, "wallet is required")
		return
	}
	role, err := auth.ParseRole(body.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var expires time.Time
	if body.ExpiresInHours != 0 {
		if body.ExpiresInHours < 0 || body.ExpiresInHours > maxGrantHours {
			writeError(w, http.StatusBadRequest,
				"expires_in_hours is between 1 and 8760; leave it out for a grant that does not expire")
			return
		}
		expires = time.Now().Add(time.Duration(body.ExpiresInHours) * time.Hour)
	}

	actor := callerWallet(r)
	if err := g.authService.Grant(r.Context(), auth.GrantRequest{
		Namespace:     ns,
		PrincipalType: auth.PrincipalWallet,
		Identifier:    body.Wallet,
		DisplayName:   strings.TrimSpace(body.DisplayName),
		Role:          role,
		Resource:      strings.TrimSpace(body.Resource),
		ExpiresAt:     expires,
		CreatedBy:     actor,
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	g.logger.ComponentInfo("gateway", "namespace member added",
		zap.String("namespace", ns), zap.String("role", string(role)))

	response := map[string]any{
		"namespace": ns,
		"wallet":    auth.NormalizeWallet(body.Wallet),
		"role":      string(role),
	}
	if resource := strings.TrimSpace(body.Resource); resource != "" {
		response["resource"] = resource
		response["enforced"] = false
		response["warning"] = "resource selectors are recorded but not enforced yet, so this grant " +
			"authorises nothing until the data plane can apply them"
	}
	writeJSON(w, http.StatusCreated, response)
}

func (g *Gateway) removeNamespaceMember(w http.ResponseWriter, r *http.Request, wallet string) {
	ns := keysNamespace(r)
	if ns == "" {
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
		return
	}
	if strings.TrimSpace(wallet) == "" {
		writeError(w, http.StatusBadRequest, "which wallet: DELETE /v1/namespace/members/{wallet}")
		return
	}

	err := g.authService.RevokeGrant(r.Context(), ns, auth.PrincipalWallet, wallet, callerWallet(r))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{
			"namespace": ns, "wallet": auth.NormalizeWallet(wallet), "status": "removed",
		})
	case strings.Contains(err.Error(), auth.ErrOwnerCannotBeRemoved.Error()):
		writeError(w, http.StatusConflict, err.Error())
	case strings.Contains(err.Error(), auth.ErrNotAMember.Error()):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (g *Gateway) transferNamespace(w http.ResponseWriter, r *http.Request) {
	ns := keysNamespace(r)
	if ns == "" {
		forbidden(w, CodeNamespaceMismatch, "the namespace this credential belongs to could not be resolved", nil)
		return
	}

	// Only the owner. An admin who could transfer could take the namespace,
	// which would make admin and owner the same thing again.
	grant := callerGrant(r)
	if grant == nil || grant.Role != auth.RoleOwner {
		forbidden(w, CodeOwnershipRequired,
			"only the namespace's owner may transfer it; an admin grant is not enough", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var body struct {
		Wallet string `json:"wallet"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: expected JSON {wallet}")
		return
	}

	if err := g.authService.TransferOwnership(r.Context(), ns, grant.Identifier, body.Wallet); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	g.logger.ComponentInfo("gateway", "namespace ownership transferred", zap.String("namespace", ns))
	writeJSON(w, http.StatusOK, map[string]any{
		"namespace":      ns,
		"owner":          auth.NormalizeWallet(body.Wallet),
		"previous_owner": grant.Identifier,
		"status":         "transferred",
	})
}

// callerGrant returns the grant the authorization middleware resolved for this
// request, or nil when the caller authenticated some other way.
func callerGrant(r *http.Request) *auth.Grant {
	grant, _ := r.Context().Value(ctxKeyGrant).(*auth.Grant)
	return grant
}

// callerWallet returns the signed-in wallet, for the record of who handed a
// grant out. An API-key caller has none, and says so rather than being recorded
// as somebody.
func callerWallet(r *http.Request) string {
	if v := r.Context().Value(ctxKeyJWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			if sub := strings.TrimSpace(claims.Sub); strings.HasPrefix(strings.ToLower(sub), "0x") {
				return auth.NormalizeWallet(sub)
			}
		}
	}
	return "an api key"
}
