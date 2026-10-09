package operator

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/archivetrust"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"go.uber.org/zap"
)

// Creator is one wallet allowed to create namespaces when creation is allowlist.
type Creator struct {
	Wallet  string `json:"wallet" db:"wallet"`
	AddedBy string `json:"added_by" db:"added_by"`
	AddedAt string `json:"added_at" db:"added_at"`
}

// HandleSettings serves the cluster settings the namespace-creation policy
// reads.
//
//	GET /v1/operator/settings
//	PUT /v1/operator/settings/{setting}
//
// {setting} is namespace-creation, max-namespaces-per-wallet, auto-update,
// update-channel, update-window or release-repo. The storage
// keys are namespace_creation and max_namespaces_per_wallet; both spellings
// are accepted. Every method requires a wallet already on the operator list.
func (h *Handler) HandleSettings(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.requireOperator(w, r)
	if !ok {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/operator/settings")
	rest = strings.Trim(rest, "/")
	switch {
	case r.Method == http.MethodGet && rest == "":
		h.getSettings(w, r)
	case r.Method == http.MethodPut && rest != "":
		h.putSetting(w, r, caller, rest)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// HandleCreators serves the allowlist consulted when namespace creation is
// allowlist.
//
//	GET    /v1/operator/creators
//	POST   /v1/operator/creators            {"wallet":"0x…"}
//	DELETE /v1/operator/creators/{wallet}
//
// An empty list denies every wallet. That is not the operator list: removing
// the last creator does not lock operators out of this endpoint.
func (h *Handler) HandleCreators(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.requireOperator(w, r)
	if !ok {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/operator/creators")
	rest = strings.TrimPrefix(rest, "/")
	switch {
	case r.Method == http.MethodGet && rest == "":
		h.listCreators(w, r)
	case r.Method == http.MethodPost && rest == "":
		h.addCreator(w, r, caller)
	case r.Method == http.MethodDelete && rest != "":
		target, err := url.PathUnescape(rest)
		if err != nil || strings.Contains(target, "/") {
			writeError(w, http.StatusBadRequest, "wallet is not a single path segment")
			return
		}
		h.removeCreator(w, r, caller, target)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	policy, err := LoadCreationPolicy(r.Context(), h.rqliteClient)
	if err != nil {
		var bad *PolicyConfigError
		if errors.As(err, &bad) {
			// The operator is the one who can replace the row. The reason
			// names the bad value; a tenant never reaches this route.
			writeError(w, http.StatusInternalServerError, bad.Error())
			return
		}
		h.logger.Error("could not read cluster settings", zap.Error(err))
		writeError(w, http.StatusServiceUnavailable, "cannot read cluster settings right now; the registry did not answer")
		return
	}
	update, err := loadUpdateSettings(r.Context(), h.rqliteClient)
	if err != nil {
		var bad *PolicyConfigError
		if errors.As(err, &bad) {
			writeError(w, http.StatusInternalServerError, bad.Error())
			return
		}
		h.logger.Error("could not read cluster settings", zap.Error(err))
		writeError(w, http.StatusServiceUnavailable, "cannot read cluster settings right now; the registry did not answer")
		return
	}
	out := map[string]any{
		SettingNamespaceCreation:      policy.Mode,
		SettingMaxNamespacesPerWallet: policy.WalletCap,
	}
	for key, value := range update {
		out[key] = value
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) putSetting(w http.ResponseWriter, r *http.Request, caller, key string) {
	var req struct {
		Value json.RawMessage `json:"value"`
	}
	if err := decodeJSON(r, &req); err != nil || len(bytesTrim(req.Value)) == 0 {
		writeError(w, http.StatusBadRequest, "value is required")
		return
	}
	storedKey, storedValue, err := normalizeSetting(key, req.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.rqliteClient.Exec(r.Context(),
		`INSERT INTO cluster_settings (key, value, updated_by) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		   value = excluded.value,
		   updated_by = excluded.updated_by,
		   updated_at = CURRENT_TIMESTAMP`,
		storedKey, storedValue, "operator:"+normaliseWallet(caller)); err != nil {
		h.logger.Error("could not store cluster setting", zap.String("key", storedKey), zap.Error(err))
		writeError(w, http.StatusInternalServerError, "could not store the setting")
		return
	}
	h.recordClusterChange(r, caller, "cluster_settings", map[string]string{
		"key":   storedKey,
		"value": storedValue,
	})
	writeJSON(w, http.StatusOK, map[string]string{"key": storedKey, "value": storedValue})
}

func (h *Handler) listCreators(w http.ResponseWriter, r *http.Request) {
	var rows []Creator
	if err := h.rqliteClient.Query(r.Context(), &rows,
		`SELECT wallet, added_by, added_at FROM namespace_creators ORDER BY added_at, wallet`); err != nil {
		h.logger.Error("could not list namespace creators", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "could not list namespace creators")
		return
	}
	if rows == nil {
		rows = []Creator{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"creators": rows})
}

func (h *Handler) addCreator(w http.ResponseWriter, r *http.Request, caller string) {
	var req struct {
		Wallet string `json:"wallet"`
	}
	if err := decodeJSON(r, &req); err != nil {
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
		`INSERT OR IGNORE INTO namespace_creators (wallet, added_by) VALUES (?, ?)`,
		target, "operator:"+normaliseWallet(caller)); err != nil {
		h.logger.Error("could not add namespace creator", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "could not add namespace creator")
		return
	}
	h.recordClusterChange(r, caller, "namespace_creators", map[string]string{
		"op":     "add",
		"wallet": target,
	})
	writeJSON(w, http.StatusOK, map[string]string{"wallet": target})
}

func (h *Handler) removeCreator(w http.ResponseWriter, r *http.Request, caller, raw string) {
	normalised, err := archivetrust.NormalizeSigners([]string{raw})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target := normalised[0]
	res, err := h.rqliteClient.Exec(r.Context(),
		`DELETE FROM namespace_creators WHERE LOWER(wallet) = ?`, target)
	if err != nil {
		h.logger.Error("could not remove namespace creator", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "could not remove namespace creator")
		return
	}
	affected, affErr := res.RowsAffected()
	if affErr != nil {
		writeError(w, http.StatusInternalServerError, "could not remove namespace creator")
		return
	}
	if affected == 0 {
		writeError(w, http.StatusNotFound, "wallet is not a namespace creator")
		return
	}
	h.recordClusterChange(r, caller, "namespace_creators", map[string]string{
		"op":     "remove",
		"wallet": target,
	})
	writeJSON(w, http.StatusOK, map[string]string{"wallet": target})
}

// recordClusterChange writes the same audit row an operator add does:
// AuditOperatorAction, through the handler's AuditLog. A refused call is not
// recorded; this runs only after the write succeeded.
func (h *Handler) recordClusterChange(r *http.Request, caller, resource string, metadata map[string]string) {
	if h.audit == nil {
		return
	}
	h.audit.RecordFromRequest(r.Context(), r, auth.AuditEvent{
		Actor:    normaliseWallet(caller),
		Action:   auth.AuditOperatorAction,
		Resource: resource,
		Result:   auth.AuditSuccess,
		Metadata: metadata,
	})
}

// normalizeSetting maps the CLI's hyphenated name onto the stored key and
// rejects a value the create handler would refuse to enforce.
func normalizeSetting(key string, raw json.RawMessage) (string, string, error) {
	if storedKey, value, ok, err := normalizeUpdateSetting(key, raw); ok {
		return storedKey, value, err
	}
	switch key {
	case "namespace-creation", SettingNamespaceCreation:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", "", fmt.Errorf("namespace-creation is operators, allowlist or open")
		}
		value = strings.TrimSpace(value)
		switch value {
		case CreationOperators, CreationAllowlist, CreationOpen:
			return SettingNamespaceCreation, value, nil
		default:
			return "", "", fmt.Errorf("namespace-creation is operators, allowlist or open")
		}
	case "max-namespaces-per-wallet", SettingMaxNamespacesPerWallet:
		n, err := parseCap(raw)
		if err != nil || n < 1 || n > MaxNamespacesPerWalletCeiling {
			return "", "", fmt.Errorf("max-namespaces-per-wallet is an integer from 1 to %d", MaxNamespacesPerWalletCeiling)
		}
		return SettingMaxNamespacesPerWallet, strconv.Itoa(n), nil
	default:
		return "", "", fmt.Errorf("unknown setting %q", key)
	}
}

func parseCap(raw json.RawMessage) (int, error) {
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(s))
}

func bytesTrim(raw json.RawMessage) string {
	return strings.TrimSpace(string(raw))
}
