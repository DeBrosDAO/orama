package namespace

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/handlers/operator"
	"go.uber.org/zap"
)

// OperatorRemoveHandler lets a cluster operator remove a namespace it does not
// own: POST /v1/operator/namespaces/remove {"namespace", "reason"}.
//
// A namespace belongs to its owner, and only the owner may delete it. When the
// owner's wallet is gone — a lost key, or a test run's throwaway wallet — the
// namespace keeps its port blocks, processes and storage on three nodes for
// ever, and nothing on the cluster could reclaim them. The operators run the
// machines those resources live on, so the removal is theirs; it is recorded
// in the audit trail with the operator's wallet and the reason given.
type OperatorRemoveHandler struct {
	deletes *DeleteHandler
	logger  *zap.Logger
}

// NewOperatorRemoveHandler returns the operator's removal, which reuses the
// owner's teardown.
func NewOperatorRemoveHandler(deletes *DeleteHandler, logger *zap.Logger) *OperatorRemoveHandler {
	return &OperatorRemoveHandler{deletes: deletes, logger: logger.With(zap.String("component", "namespace-operator-remove"))}
}

// OperatorRemoveRequest is the body of POST /v1/operator/namespaces/remove.
type OperatorRemoveRequest struct {
	Namespace string `json:"namespace"`
	// Reason is recorded in the audit trail; required, so the record says why.
	Reason string `json:"reason"`
}

// maxRemoveReason bounds the reason recorded.
const maxRemoveReason = 500

func (h *OperatorRemoveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeDeleteResponse(w, http.StatusMethodNotAllowed, map[string]interface{}{"error": "method not allowed"})
		return
	}
	wallet, ok := h.requireOperator(w, r)
	if !ok {
		return
	}
	req, ok := decodeRemoveRequest(w, r)
	if !ok {
		return
	}
	h.logger.Warn("An operator is removing a namespace",
		zap.String("namespace", req.Namespace), zap.String("operator", wallet), zap.String("reason", req.Reason))
	h.deletes.remove(w, r, req.Namespace, auth.AuditNamespaceRemovedByOperator,
		map[string]string{"operator": wallet, "reason": req.Reason})
}

// requireOperator answers the refusal and reports false unless the caller is
// a signed-in wallet on the cluster's operator list.
func (h *OperatorRemoveHandler) requireOperator(w http.ResponseWriter, r *http.Request) (string, bool) {
	wallet := walletFromContext(r)
	if wallet == "" {
		writeDeleteResponse(w, http.StatusUnauthorized, map[string]interface{}{"error": "removing a namespace requires an operator's signed-in wallet"})
		return "", false
	}
	isOperator, err := operator.IsOperator(r.Context(), h.deletes.ormClient, wallet)
	if err != nil {
		h.logger.Error("could not read the operator list", zap.Error(err))
		writeDeleteResponse(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "cannot verify operator status right now; the registry did not answer", "retryable": true})
		return "", false
	}
	if !isOperator {
		writeDeleteResponse(w, http.StatusForbidden, map[string]interface{}{
			"error": "wallet " + wallet + " is not an operator of this cluster",
			"code":  operator.ErrCodeNotAnOperator,
		})
		return "", false
	}
	return wallet, true
}

// decodeRemoveRequest reads and validates the body, answering the refusal.
func decodeRemoveRequest(w http.ResponseWriter, r *http.Request) (OperatorRemoveRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	var req OperatorRemoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDeleteResponse(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid json body: expected {namespace, reason}"})
		return req, false
	}
	req.Namespace = strings.ToLower(strings.TrimSpace(req.Namespace))
	req.Reason = strings.TrimSpace(req.Reason)
	switch {
	case !namespaceName.MatchString(req.Namespace):
		writeDeleteResponse(w, http.StatusBadRequest, map[string]interface{}{"error": "namespace is not a namespace name", "code": ErrCodeNamespaceName})
	case reservedNamespaces[req.Namespace] || auth.IsLobbyNamespace(req.Namespace):
		writeDeleteResponse(w, http.StatusBadRequest, map[string]interface{}{"error": "namespace " + req.Namespace + " is the platform's own and cannot be removed"})
	case req.Reason == "" || len(req.Reason) > maxRemoveReason:
		writeDeleteResponse(w, http.StatusBadRequest, map[string]interface{}{"error": "a reason (1 to 500 characters) is required; it is recorded in the audit trail"})
	default:
		return req, true
	}
	return req, false
}
