package namespace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
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
//
// Who may call it is a cluster setting, not a property of the route. The route
// stays a wallet token with no grant, because a wallet with no namespace holds
// none and open mode is exactly "any signed-in wallet". operators checks the
// operators table; allowlist checks namespace_creators. A missing setting means
// operators, which is a new cluster. Migration 063 writes open when the
// registry already had data, so an upgrade does not take creation away.

const (
	// ErrCodeNamespaceTaken is returned when the name already exists.
	ErrCodeNamespaceTaken = "NAMESPACE_TAKEN"
	// ErrCodeNamespaceQuota is returned when the wallet has as many namespaces
	// as it is allowed.
	ErrCodeNamespaceQuota = "NAMESPACE_QUOTA"
	// ErrCodeNamespaceCreation is returned when this cluster does not let the
	// caller's wallet create a namespace.
	ErrCodeNamespaceCreation = "NAMESPACE_CREATION_DENIED"
	// ErrCodeNamespaceName is returned when the name is not a legal one.
	ErrCodeNamespaceName = "NAMESPACE_NAME_INVALID"
	// ErrCodeNamespaceProvision is returned when the namespace's cluster could
	// not be started and the create was undone.
	ErrCodeNamespaceProvision = "NAMESPACE_PROVISION_FAILED"
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
	// Platform DNS labels. These used to live in a reserved_domains table
	// that nothing ever read. A namespace of this name becomes ns-<name>
	// and also collides with hosts the nameserver already answers.
	"api": true, "www": true, "mail": true, "cdn": true, "docs": true,
	"status": true, "push": true, "turn": true,
	"ns1": true, "ns2": true, "ns3": true, "ns4": true,
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

	// Policy before the existence check, so a wallet the cluster does not
	// allow cannot learn whether a name is taken.
	policy, err := operator.LoadCreationPolicy(ctx, h.ormClient)
	if err != nil {
		h.refuseCreationPolicy(w, err)
		return
	}
	allowed, err := policy.Permits(ctx, h.ormClient, wallet)
	if err != nil {
		h.refuseCreationPolicy(w, err)
		return
	}
	if !allowed {
		writeCreateJSON(w, http.StatusForbidden, map[string]any{
			"error": creationDenied(policy.Mode),
			"code":  ErrCodeNamespaceCreation,
		})
		return
	}

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
	// Ten unless an operator stored a different cap. A stored value this
	// binary does not understand was already refused above, not treated as
	// unlimited.
	if owned >= policy.WalletCap {
		writeNamespaceQuota(w, policy.WalletCap)
		return
	}

	namespaceID, err := h.create(ctx, name, wallet, policy.WalletCap)
	if errors.Is(err, errNamespaceQuota) {
		// Other creates by this wallet committed between the count above and
		// this one's owner grant.
		writeNamespaceQuota(w, policy.WalletCap)
		return
	}
	if errors.Is(err, errNamespaceTaken) {
		// Another create of the same name committed between the check above
		// and this insert: the same answer the check gives.
		writeCreateJSON(w, http.StatusConflict, map[string]any{
			"error": "namespace " + name + " already exists",
			"code":  ErrCodeNamespaceTaken,
		})
		return
	}
	if err != nil {
		h.logger.Error("could not create the namespace", zap.String("namespace", name), zap.Error(err))
		writeCreateJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "failed to create the namespace",
		})
		return
	}

	recordCreated := func() {
		h.audit.RecordFromRequest(ctx, r, auth.AuditEvent{
			Namespace: name,
			Actor:     wallet,
			Action:    auth.AuditNamespaceCreated,
			Result:    auth.AuditSuccess,
		})
	}

	response := map[string]any{"name": name, "owner": wallet, "status": "created"}

	// Provisioning is what makes a namespace real, and it starts here rather
	// than on a login. A gateway with no provisioner records the namespace and
	// says so, instead of reporting a cluster that will never appear.
	if h.provisioner != nil {
		clusterID, pollURL, err := h.provisioner.ProvisionNamespaceCluster(ctx, int(namespaceID), name, wallet)
		if err != nil {
			h.refuseUnprovisioned(w, r, namespaceID, name, err)
			return
		}
		recordCreated()
		response["status"] = "provisioning"
		response["cluster_id"] = clusterID
		response["poll_url"] = pollURL
		response["estimated_time_seconds"] = 60
		writeCreateJSON(w, http.StatusAccepted, response)
		return
	}

	recordCreated()
	writeCreateJSON(w, http.StatusCreated, response)
}

// refuseUnprovisioned undoes a create whose cluster could not be started, and
// answers 503. A namespace row and owner grant with no cluster behind them
// cannot be used, are counted against the wallet's cap, and keep the name
// taken, and nothing but a delete would ever remove them.
//
// If the undo itself fails, the namespace stays and the answer says so: it is
// then a namespace without a cluster, which its owner's delete removes.
func (h *CreateHandler) refuseUnprovisioned(w http.ResponseWriter, r *http.Request, namespaceID int64, name string, cause error) {
	h.logger.Error("namespace created but provisioning did not start",
		zap.String("namespace", name), zap.Error(cause))
	if err := h.removeUnprovisioned(r.Context(), namespaceID); err != nil {
		h.logger.Error("could not undo the create of a namespace whose provisioning did not start",
			zap.String("namespace", name), zap.Error(err))
		h.audit.RecordFromRequest(r.Context(), r, auth.AuditEvent{
			Namespace: name,
			Actor:     walletFromContext(r),
			Action:    auth.AuditNamespaceCreated,
			Result:    auth.AuditSuccess,
		})
		writeCreateJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "the namespace's cluster could not be started (" + cause.Error() + ") and the namespace " +
				"could not be removed again; delete namespace " + name + " to clear it",
		})
		return
	}
	writeCreateJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error": "the namespace's cluster could not be started, so nothing was created: " + cause.Error(),
		"code":  ErrCodeNamespaceProvision,
	})
}

// removeUnprovisioned deletes a namespace's owner grants and its row, unless a
// cluster row exists for it: provisioning that failed after recording its
// cluster is a cluster the owner's delete deprovisions, not something to drop
// from under it. The guard is in each statement, as the cap is in the grant's.
func (h *CreateHandler) removeUnprovisioned(ctx context.Context, namespaceID int64) error {
	const noCluster = " AND NOT EXISTS (SELECT 1 FROM namespace_clusters WHERE namespace_id = ?)"
	if _, err := h.ormClient.Exec(ctx, "DELETE FROM grants WHERE namespace_id = ?"+noCluster, namespaceID, namespaceID); err != nil {
		return fmt.Errorf("delete the grants of namespace %d: %w", namespaceID, err)
	}
	res, err := h.ormClient.Exec(ctx, "DELETE FROM namespaces WHERE id = ?"+noCluster, namespaceID, namespaceID)
	if err != nil {
		return fmt.Errorf("delete namespace %d: %w", namespaceID, err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete namespace %d: could not read whether the row was removed: %w", namespaceID, err)
	}
	if removed == 0 {
		return fmt.Errorf("namespace %d has a cluster record, so it was kept", namespaceID)
	}
	return nil
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
//
// The existence check before it does not hold across concurrent creates of
// one name, so the insert itself decides: the name is UNIQUE, the insert
// ignores a conflict, and a create that inserted nothing lost the race and
// returns errNamespaceTaken without writing an owner.
func (h *CreateHandler) create(ctx context.Context, name, wallet string, walletCap int) (int64, error) {
	res, err := h.ormClient.Exec(ctx, "INSERT OR IGNORE INTO namespaces(name) VALUES (?)", name)
	if err != nil {
		return 0, fmt.Errorf("insert namespace: %w", err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("insert namespace: could not read whether the row was written: %w", err)
	}
	if inserted == 0 {
		return 0, errNamespaceTaken
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
	// The per-wallet cap is decided by this statement, not by the count the
	// handler made first: concurrent creates by one wallet all passed that
	// count, and a wallet capped at ten was seen owning twelve. Raft applies
	// one statement at a time, so the count in its WHERE sees every grant
	// committed before it.
	res, err = h.ormClient.Exec(ctx, ownerGrantUnderCap, rows[0].ID, owner, owner, owner, walletCap)
	if err != nil {
		return 0, fmt.Errorf("the namespace was created but its owner grant could not be recorded, "+
			"so it would be unowned and claimable: %w", err)
	}
	granted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("the namespace was created but whether its owner grant was written could not be read, "+
			"so it may be unowned and claimable: %w", err)
	}
	if granted == 0 {
		if _, err := h.ormClient.Exec(ctx, "DELETE FROM namespaces WHERE id = ?", rows[0].ID); err != nil {
			return 0, fmt.Errorf("the wallet is at its namespace limit, and the namespace row created for it "+
				"could not be removed, so it is unowned: %w", err)
		}
		return 0, errNamespaceQuota
	}
	return rows[0].ID, nil
}

// ownerGrantUnderCap writes a namespace's owner grant only while the wallet
// owns fewer than the cap: args are the namespace id, the owner (created_by),
// the owner (principal), the owner (counted) and the cap.
const ownerGrantUnderCap = `INSERT INTO grants(principal_id, namespace_id, role, created_by)
		 SELECT id, ?, 'owner', ? FROM principals WHERE type = 'wallet' AND identifier = ?
		   AND (SELECT COUNT(*) FROM grants g JOIN principals p ON p.id = g.principal_id
		         WHERE p.type = 'wallet' AND p.identifier = ? AND g.role = 'owner' AND g.revoked_at IS NULL) < ?`

// errNamespaceQuota is create's answer when the wallet reached its cap
// between the handler's count and the owner grant.
var errNamespaceQuota = errors.New("the wallet owns as many namespaces as it may")

// writeNamespaceQuota answers a wallet at its namespace cap. It names the
// cap: it used to name the wallet's count as "the limit", and a count past
// the cap read as a second limit.
func writeNamespaceQuota(w http.ResponseWriter, walletCap int) {
	writeCreateJSON(w, http.StatusForbidden, map[string]any{
		"error": fmt.Sprintf("this wallet already owns the most namespaces it may (%d); "+
			"delete one to create another", walletCap),
		"code": ErrCodeNamespaceQuota,
	})
}

// errNamespaceTaken is create's answer when another create of the same name
// won the insert.
var errNamespaceTaken = errors.New("namespace already exists")

func creationDenied(mode string) string {
	switch mode {
	case operator.CreationOperators:
		return "only an operator of this cluster may create a namespace"
	case operator.CreationAllowlist:
		return "only a wallet on this cluster's namespace-creator list may create a namespace"
	default:
		return "this wallet may not create namespaces on this cluster"
	}
}

// refuseCreationPolicy answers 503 and writes nothing.
//
// A stored value this binary cannot enforce is not the same failure as a
// registry that did not answer, and neither one is permission to create.
func (h *CreateHandler) refuseCreationPolicy(w http.ResponseWriter, err error) {
	var bad *operator.PolicyConfigError
	if errors.As(err, &bad) {
		h.logger.Error("namespace creation setting is not one this gateway can enforce", zap.Error(err))
		writeCreateJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "namespace creation is not configured; an operator has to correct the cluster setting",
		})
		return
	}
	h.logger.Error("could not read the namespace creation policy", zap.Error(err))
	writeCreateJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error": "the registry did not answer; try again",
	})
}

func writeCreateJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
