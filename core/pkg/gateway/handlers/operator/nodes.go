package operator

import (
	"net/http"

	"go.uber.org/zap"
)

// NodeInfo represents a node owned by the operator.
type NodeInfo struct {
	ID             string `json:"id" db:"id"`
	IPAddress      string `json:"ip_address" db:"ip_address"`
	InternalIP     string `json:"internal_ip,omitempty" db:"internal_ip"`
	Environment    string `json:"environment,omitempty" db:"environment"`
	Role           string `json:"role,omitempty" db:"role"`
	SSHUser        string `json:"ssh_user,omitempty" db:"ssh_user"`
	Status         string `json:"status" db:"status"`
	Region         string `json:"region,omitempty" db:"region"`
	LastSeen       string `json:"last_seen,omitempty" db:"last_seen"`
	OperatorWallet string `json:"operator_wallet,omitempty" db:"operator_wallet"`
}

// ListNodesResponse is returned by GET /v1/operator/nodes.
type ListNodesResponse struct {
	Nodes []NodeInfo `json:"nodes"`
}

// HandleListNodes returns all nodes owned by the authenticated operator.
// Optionally filtered by ?env=<environment>.
//
// GET /v1/operator/nodes
func (h *Handler) HandleListNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	wallet, ok := h.requireOperator(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	envFilter := r.URL.Query().Get("env")

	query := `SELECT id, ip_address, COALESCE(internal_ip, '') AS internal_ip,
		COALESCE(environment, 'production') AS environment,
		COALESCE(role, 'node') AS role, COALESCE(ssh_user, 'root') AS ssh_user,
		status, COALESCE(region, '') AS region, COALESCE(last_seen, '') AS last_seen,
		COALESCE(operator_wallet, '') AS operator_wallet
		FROM dns_nodes WHERE operator_wallet = ?`
	args := []interface{}{wallet}

	if envFilter != "" {
		query += " AND environment = ?"
		args = append(args, envFilter)
	}

	query += " ORDER BY environment, ip_address"

	var nodes []NodeInfo
	if err := h.rqliteClient.Query(ctx, &nodes, query, args...); err != nil {
		h.logger.Error("failed to query operator nodes", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to query nodes")
		return
	}

	if nodes == nil {
		nodes = []NodeInfo{}
	}

	writeJSON(w, http.StatusOK, ListNodesResponse{Nodes: nodes})
}
