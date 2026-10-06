package operator

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"go.uber.org/zap"
)

// Operator is one wallet allowed to operate this cluster.
type Operator struct {
	Wallet  string `json:"wallet" db:"wallet"`
	AddedBy string `json:"added_by" db:"added_by"`
	AddedAt string `json:"added_at" db:"added_at"`
}

// HandleOperators serves the operator list.
//
//	GET    /v1/operator/operators
//	POST   /v1/operator/operators          {"wallet":"0x…"}
//	DELETE /v1/operator/operators/{wallet}
//
// Every method requires a wallet that is already on the list. Removing the
// last operator is refused: an empty list locks the cluster out of every
// operator endpoint, and nothing in the API could put a wallet back.
func (h *Handler) HandleOperators(w http.ResponseWriter, r *http.Request) {
	wallet, ok := h.requireOperator(w, r)
	if !ok {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/operator/operators")
	rest = strings.TrimPrefix(rest, "/")
	switch {
	case r.Method == http.MethodGet && rest == "":
		h.listOperators(w, r)
	case r.Method == http.MethodPost && rest == "":
		h.addOperator(w, r, wallet)
	case r.Method == http.MethodDelete && rest != "":
		target, err := url.PathUnescape(rest)
		if err != nil || strings.Contains(target, "/") {
			writeError(w, http.StatusBadRequest, "wallet is not a single path segment")
			return
		}
		h.removeOperator(w, r, wallet, target)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listOperators(w http.ResponseWriter, r *http.Request) {
	var rows []Operator
	if err := h.rqliteClient.Query(r.Context(), &rows,
		`SELECT wallet, added_by, added_at FROM operators ORDER BY added_at, wallet`); err != nil {
		h.logger.Error("could not list operators", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "could not list operators")
		return
	}
	if rows == nil {
		rows = []Operator{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"operators": rows})
}

func (h *Handler) addOperator(w http.ResponseWriter, r *http.Request, caller string) {
	var req struct {
		Wallet string `json:"wallet"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "wallet is required")
		return
	}
	normalised, err := archivetrust.NormalizeSigners([]string{req.Wallet})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target := normalised[0]
	if _, err := h.rqliteClient.Exec(r.Context(),
		`INSERT OR IGNORE INTO operators (wallet, added_by) VALUES (?, ?)`,
		target, "operator:"+normaliseWallet(caller)); err != nil {
		h.logger.Error("could not add operator", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "could not add operator")
		return
	}
	h.recordOperatorChange(r, caller, "add", target)
	writeJSON(w, http.StatusOK, map[string]string{"wallet": target})
}

func (h *Handler) removeOperator(w http.ResponseWriter, r *http.Request, caller, raw string) {
	target := normaliseWallet(raw)
	if target == "" {
		writeError(w, http.StatusBadRequest, "wallet is required")
		return
	}
	// One statement, so two removals cannot both observe "more than one" and
	// leave the list empty. A delete that matches nothing is either "not on
	// the list" or "this is the last one"; the follow-up read says which.
	res, err := h.rqliteClient.Exec(r.Context(),
		`DELETE FROM operators WHERE LOWER(wallet) = ? AND (SELECT COUNT(*) FROM operators) > 1`,
		target)
	if err != nil {
		h.logger.Error("could not remove operator", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "could not remove operator")
		return
	}
	affected, affErr := res.RowsAffected()
	if affErr != nil {
		writeError(w, http.StatusInternalServerError, "could not remove operator")
		return
	}
	if affected == 0 {
		onList, err := h.isOperator(r.Context(), target)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "cannot verify operator status right now")
			return
		}
		if onList {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "refusing to remove the last operator",
				"code":  "LAST_OPERATOR",
			})
			return
		}
		writeError(w, http.StatusNotFound, "wallet is not an operator")
		return
	}
	h.recordOperatorChange(r, caller, "remove", target)
	writeJSON(w, http.StatusOK, map[string]string{"wallet": target})
}

func (h *Handler) recordOperatorChange(r *http.Request, caller, op, target string) {
	if h.audit == nil {
		return
	}
	h.audit.RecordFromRequest(r.Context(), r, auth.AuditEvent{
		Actor:    normaliseWallet(caller),
		Action:   auth.AuditOperatorAction,
		Resource: "operators",
		Result:   auth.AuditSuccess,
		Metadata: map[string]string{"op": op, "wallet": target},
	})
}

func normaliseWallet(wallet string) string {
	return strings.ToLower(strings.TrimSpace(wallet))
}
