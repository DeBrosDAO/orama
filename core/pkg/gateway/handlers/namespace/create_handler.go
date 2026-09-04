package namespace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

// Creating a namespace used to be a side effect of asking for a login
// challenge: an unauthenticated POST to /v1/auth/challenge inserted a row for
// whatever name it was given, and verifying the signature then triggered real
// cluster provisioning. Squatting a name was free, and an anonymous caller
// could create infrastructure.
//
// It is a deliberate, authenticated act now: this endpoint. It writes the
// namespace and its single owner grant together, applies a per-wallet quota,
// and is the only thing that starts provisioning.

const (
	// ErrCodeNamespaceTaken is returned when the name already exists.
	ErrCodeNamespaceTaken = "NAMESPACE_TAKEN"
	// ErrCodeNamespaceQuota is returned when the wallet has as many namespaces
	// as it is allowed.
	ErrCodeNamespaceQuota = "NAMESPACE_QUOTA"
	// ErrCodeNamespaceName is returned when the name is not a legal one.
	ErrCodeNamespaceName = "NAMESPACE_NAME_INVALID"

	// maxNamespacesPerWallet caps how many namespaces one wallet may create.
	//
	// Each one is a cluster: rqlite, Olric, a gateway, a share of the mesh.
	// There was no limit at all, and no cost, so a single wallet could ask for
	// as many as it liked and each one was real.
	maxNamespacesPerWallet = 10
)

// namespaceName is what a namespace may be called.
//
// It becomes a DNS label (ns-<name>.<base domain>), a systemd instance name
// (orama-namespace-rqlite@<name>) and a directory, so it is held to what all
// three accept: lowercase letters, digits and hyphens, not starting or ending
// with a hyphen.
var namespaceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)

// reservedNamespaces are names the platform uses for itself.
var reservedNamespaces = map[string]bool{
	"default": true, "index": true, "nameserver": true,
	"system": true, "orama": true, "admin": true, "internal": true,
}

// Provisioner starts a namespace's cluster. Satisfied by the gateway's cluster
// provisioner; nil in a gateway that does not provision, where creating a
// namespace records it without spawning anything.
type Provisioner interface {
	ProvisionNamespaceCluster(ctx context.Context, namespaceID int, namespace, ownerWallet string) (clusterID string, pollURL string, err error)
}

// CreateHandler handles POST /v1/namespaces.
type CreateHandler struct {
	ormClient   rqlite.Client
	provisioner Provisioner
	audit       *auth.AuditLog
	logger      *zap.Logger
}

// NewCreateHandler creates the namespace-creation handler.
func NewCreateHandler(orm rqlite.Client, provisioner Provisioner, audit *auth.AuditLog, logger *zap.Logger) *CreateHandler {
	return &CreateHandler{
		ormClient:   orm,
		provisioner: provisioner,
		audit:       audit,
		logger:      logger.With(zap.String("component", "namespace-create-handler")),
	}
}

// CreateRequest is the body of POST /v1/namespaces.
type CreateRequest struct {
	Name string `json:"name"`
}

func (h *CreateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeCreateJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}

	wallet := walletFromContext(r)
	if wallet == "" {
		writeCreateJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "creating a namespace requires a signed-in wallet",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCreateJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json body"})
		return
	}

	name := strings.ToLower(strings.TrimSpace(req.Name))
	if !namespaceName.MatchString(name) {
		writeCreateJSON(w, http.StatusBadRequest, map[string]any{
			"error": "namespace names are 2 to 40 characters of lowercase letters, digits and " +
				"hyphens, not starting or ending with a hyphen: they become a DNS label, a " +
				"systemd instance name and a directory",
			"code": ErrCodeNamespaceName,
		})
		return
	}
	if reservedNamespaces[name] {
		writeCreateJSON(w, http.StatusBadRequest, map[string]any{
			"error": "namespace " + name + " is reserved by the platform",
			"code":  ErrCodeNamespaceName,
		})
		return
	}

	ctx := r.Context()

	taken, err := h.exists(ctx, name)
	if err != nil {
		h.logger.Error("could not check whether the namespace exists", zap.Error(err))
		writeCreateJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "the registry did not answer; try again",
		})
		return
	}
	if taken {
		writeCreateJSON(w, http.StatusConflict, map[string]any{
			"error": "namespace " + name + " already exists",
			"code":  ErrCodeNamespaceTaken,
		})
		return
	}

	owned, err := h.countOwned(ctx, wallet)
	if err != nil {
		h.logger.Error("could not count the wallet's namespaces", zap.Error(err))
		writeCreateJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "the registry did not answer; try again",
		})
		return
	}
	if owned >= maxNamespacesPerWallet {
		writeCreateJSON(w, http.StatusForbidden, map[string]any{
			"error": fmt.Sprintf("this wallet already owns %d namespaces, which is the limit; "+
				"delete one to create another", owned),
			"code": ErrCodeNamespaceQuota,
		})
		return
	}

	namespaceID, err := h.create(ctx, name, wallet)
	if err != nil {
		h.logger.Error("could not create the namespace", zap.String("namespace", name), zap.Error(err))
		writeCreateJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to create the namespace",
		})
		return
	}

	h.audit.RecordFromRequest(ctx, r, auth.AuditEvent{
		Namespace: name,
		Actor:     wallet,
		Action:    auth.AuditNamespaceCreated,
		Result:    auth.AuditSuccess,
	})

	response := map[string]any{"name": name, "owner": wallet, "status": "created"}

	// Provisioning is what makes a namespace real, and it starts here rather
	// than on a login. A gateway with no provisioner records the namespace and
	// says so, instead of reporting a cluster that will never appear.
	if h.provisioner != nil {
		clusterID, pollURL, err := h.provisioner.ProvisionNamespaceCluster(ctx, int(namespaceID), name, wallet)
		if err != nil {
			h.logger.Error("namespace created but provisioning did not start",
				zap.String("namespace", name), zap.Error(err))
			response["status"] = "created"
			response["cluster"] = "not started: " + err.Error()
			writeCreateJSON(w, http.StatusAccepted, response)
			return
		}
		response["status"] = "provisioning"
		response["cluster_id"] = clusterID
		response["poll_url"] = pollURL
		response["estimated_time_seconds"] = 60
		writeCreateJSON(w, http.StatusAccepted, response)
		return
	}

	writeCreateJSON(w, http.StatusCreated, response)
}

// walletFromContext returns the signed-in wallet, or "" when the caller
// authenticated some other way.
//
// A namespace's owner is a wallet, so a key-authenticated caller cannot create
// one: there would be nobody to record as the owner, and a namespace with no
// owner is claimable by whoever signs in to it next — the shape of the bug this
// replaces. A JWT whose subject is an API key is not a wallet.
func walletFromContext(r *http.Request) string {
	claims, ok := r.Context().Value(ctxkeys.JWT).(*auth.JWTClaims)
	if !ok || claims == nil {
		return ""
	}
	sub := strings.TrimSpace(claims.Sub)
	if !strings.HasPrefix(strings.ToLower(sub), "0x") {
		return ""
	}
	return sub
}

func (h *CreateHandler) exists(ctx context.Context, name string) (bool, error) {
	var rows []struct {
		ID int64 `db:"id"`
	}
	if err := h.ormClient.Query(ctx, &rows,
		"SELECT id FROM namespaces WHERE name = ? LIMIT 1", name); err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

func (h *CreateHandler) countOwned(ctx context.Context, wallet string) (int, error) {
	var rows []struct {
		N int `db:"n"`
	}
	if err := h.ormClient.Query(ctx, &rows,
		`SELECT COUNT(*) AS n
		   FROM grants g JOIN principals p ON p.id = g.principal_id
		  WHERE p.type = 'wallet' AND p.identifier = ?
		    AND g.role = 'owner' AND g.revoked_at IS NULL`,
		auth.NormalizeWallet(wallet)); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].N, nil
}

// create writes the namespace, its owner principal and the owner grant.
//
// The grant is what makes the namespace someone's. Writing the namespace
// without it would leave a row anybody could then claim by signing in, which
// is the shape of the bug this replaces.
func (h *CreateHandler) create(ctx context.Context, name, wallet string) (int64, error) {
	if _, err := h.ormClient.Exec(ctx, "INSERT INTO namespaces(name) VALUES (?)", name); err != nil {
		return 0, fmt.Errorf("insert namespace: %w", err)
	}

	var rows []struct {
		ID int64 `db:"id"`
	}
	if err := h.ormClient.Query(ctx, &rows,
		"SELECT id FROM namespaces WHERE name = ? LIMIT 1", name); err != nil || len(rows) == 0 {
		return 0, fmt.Errorf("the namespace was created but its id could not be read back: %w", err)
	}

	owner := auth.NormalizeWallet(wallet)
	if _, err := h.ormClient.Exec(ctx,
		"INSERT OR IGNORE INTO principals(type, identifier, created_by) VALUES ('wallet', ?, ?)",
		owner, owner); err != nil {
		return 0, fmt.Errorf("the namespace was created but its owner could not be recorded, "+
			"so it would be unowned and claimable: %w", err)
	}
	if _, err := h.ormClient.Exec(ctx,
		`INSERT INTO grants(principal_id, namespace_id, role, created_by)
		 SELECT id, ?, 'owner', ? FROM principals WHERE type = 'wallet' AND identifier = ?`,
		rows[0].ID, owner, owner); err != nil {
		return 0, fmt.Errorf("the namespace was created but its owner grant could not be recorded, "+
			"so it would be unowned and claimable: %w", err)
	}
	return rows[0].ID, nil
}

func writeCreateJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
