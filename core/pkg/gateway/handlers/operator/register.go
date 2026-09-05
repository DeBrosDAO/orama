package operator

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"go.uber.org/zap"
)

// RegisterRequest is the body for POST /v1/operator/node/register.
type RegisterRequest struct {
	NodeID      string `json:"node_id"`               // dns_nodes.id (peer ID or hostname)
	IPAddress   string `json:"ip_address,omitempty"`  // Public IP (alternative lookup key)
	Environment string `json:"environment,omitempty"` // e.g., "devnet", "sandbox"
	Role        string `json:"role,omitempty"`        // e.g., "node", "nameserver"
	SSHUser     string `json:"ssh_user,omitempty"`    // SSH user (default: "root")
}

var (
	allowedEnvironments = map[string]bool{
		"production": true, "devnet": true, "testnet": true, "sandbox": true, "mainnet": true,
	}
	allowedRoles = map[string]bool{
		"node": true, "nameserver": true, "nameserver-ns1": true, "nameserver-ns2": true, "nameserver-ns3": true,
	}
)

// HandleRegister tags an existing node with the operator's wallet.
// The node must already exist in dns_nodes and be either unclaimed or
// already owned by the requesting operator.
//
// POST /v1/operator/node/register
func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	wallet, ok := h.requireOperator(w, r)
	if !ok {
		return
	}

	var req RegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.NodeID == "" && req.IPAddress == "" {
		writeError(w, http.StatusBadRequest, "node_id or ip_address required")
		return
	}
	if req.Environment != "" && !allowedEnvironments[req.Environment] {
		writeError(w, http.StatusBadRequest, "invalid environment")
		return
	}
	if req.Role != "" && !allowedRoles[req.Role] {
		writeError(w, http.StatusBadRequest, "invalid role")
		return
	}

	ctx := r.Context()

	// Build the UPDATE dynamically based on what fields are provided.
	setClauses := "operator_wallet = ?"
	args := []interface{}{wallet}

	if req.Environment != "" {
		setClauses += ", environment = ?"
		args = append(args, req.Environment)
	}
	if req.Role != "" {
		setClauses += ", role = ?"
		args = append(args, req.Role)
	}
	if req.SSHUser != "" {
		setClauses += ", ssh_user = ?"
		args = append(args, req.SSHUser)
	}

	setClauses += ", updated_at = datetime('now')"

	// Match by node_id or ip_address. Only allow claiming unclaimed nodes
	// or nodes already owned by this operator (prevents hijacking).
	var whereClause string
	if req.NodeID != "" {
		whereClause = "id = ? AND (operator_wallet IS NULL OR operator_wallet = '' OR operator_wallet = ?)"
		args = append(args, req.NodeID, wallet)
	} else {
		whereClause = "ip_address = ? AND (operator_wallet IS NULL OR operator_wallet = '' OR operator_wallet = ?)"
		args = append(args, req.IPAddress, wallet)
	}

	query := "UPDATE dns_nodes SET " + setClauses + " WHERE " + whereClause
	result, err := h.rqliteClient.Exec(ctx, query, args...)
	if err != nil {
		h.logger.Error("failed to register node with operator", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to register node")
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		h.logger.Error("failed to check rows affected", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to register node")
		return
	}
	if rowsAffected == 0 {
		writeError(w, http.StatusNotFound, "node not found or owned by another operator")
		return
	}

	// Also update wireguard_peers if we can match by public_ip.
	if req.IPAddress != "" {
		if _, err := h.rqliteClient.Exec(ctx,
			"UPDATE wireguard_peers SET operator_wallet = ? WHERE public_ip = ?",
			wallet, req.IPAddress); err != nil {
			h.logger.Warn("failed to update operator_wallet on wireguard_peers", zap.Error(err))
		}
	}

	// Claiming a node is what puts it under an operator's control.
	h.audit.RecordFromRequest(ctx, r, auth.AuditEvent{
		Actor:    wallet,
		Action:   auth.AuditOperatorAction,
		Resource: "node.register",
		Result:   auth.AuditSuccess,
		Metadata: map[string]string{"node_id": req.NodeID, "ip_address": req.IPAddress},
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "registered",
		"wallet":  wallet,
		"node_id": req.NodeID,
	})
}

func decodeJSON(r *http.Request, v interface{}) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
