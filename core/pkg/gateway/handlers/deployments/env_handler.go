package deployments

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/deployments/process"
	"go.uber.org/zap"
)

// EnvHandler reads and writes a deployment's environment variables.
//
// The variables were settable only at deploy time, and only for Go apps, so
// changing a database URL or an API key meant redeploying the whole build. The
// guide told people to "use environment variables for secrets" while no command
// could set one.
type EnvHandler struct {
	service        *DeploymentService
	processManager *process.Manager
	logger         *zap.Logger
	baseDeployPath string
}

// NewEnvHandler creates a new environment handler.
func NewEnvHandler(service *DeploymentService, processManager *process.Manager, logger *zap.Logger, baseDeployPath string) *EnvHandler {
	return &EnvHandler{
		service:        service,
		processManager: processManager,
		logger:         logger,
		baseDeployPath: baseDeployPath,
	}
}

// reservedEnvKeys are set by the platform and must not be overwritten.
//
// PORT is written into the unit by the process manager and is how the gateway
// reaches the app; ENTRY_POINT is how a Node.js deployment knows what to run.
// Letting a user set either would break the deployment in a way that looks like
// their own code failing. The ORAMA_* names are the deployment's own identity —
// which namespace it belongs to, which gateway is its own, where it may write —
// and a deployment that could rewrite them could point itself at another
// namespace's gateway.
var reservedEnvKeys = buildReservedEnvKeys()

func buildReservedEnvKeys() map[string]bool {
	reserved := map[string]bool{"ENTRY_POINT": true}
	for _, key := range process.PlatformEnvKeys {
		reserved[key] = true
	}
	return reserved
}

// HandleGetEnv returns the deployment's environment variable names.
// GET /v1/deployments/env?name=<deployment>
//
// Values are never returned. They are the place the platform tells people to
// put their secrets, so an endpoint that echoes them puts every secret behind
// nothing more than a read scope, and into whatever terminal scrollback or CI
// log the caller happens to be writing to.
func (h *EnvHandler) HandleGetEnv(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	deployment, err := h.service.GetDeployment(ctx, namespace, name)
	if err != nil {
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}

	keys := make([]string, 0, len(deployment.Environment))
	for key := range deployment.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	vars := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		vars = append(vars, map[string]any{
			"key":      key,
			"reserved": reservedEnvKeys[key],
		})
	}

	writeJSON(w, map[string]any{
		"deployment_name": deployment.Name,
		"variables":       vars,
		"total":           len(vars),
	})
}

// HandleSetEnv applies a set of changes to the deployment's environment.
// POST /v1/deployments/env/set?name=<deployment> {"set": {...}, "unset": [...]}
//
// This runs on the deployment's home node, behind withHomeNodeProxy: the
// environment lives in a systemd unit on the machine the process runs on, so
// the write and the unit rewrite have to happen in the same place.
//
// The deployment name is in the query string, not the body, because that is
// where withHomeNodeProxy looks for it. A name only in the body would leave the
// proxy unable to tell which node owns the deployment, and the request would be
// served by whichever node the client happened to reach — writing the row but
// rewriting a unit file on the wrong machine.
func (h *EnvHandler) HandleSetEnv(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	ctx := r.Context()
	namespace := getNamespaceFromContext(ctx)
	if namespace == "" {
		http.Error(w, "Namespace not found in context", http.StatusUnauthorized)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name query parameter is required", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Set   map[string]string `json:"set"`
		Unset []string          `json:"unset"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Set) == 0 && len(req.Unset) == 0 {
		http.Error(w, "nothing to change: give set or unset", http.StatusBadRequest)
		return
	}

	deployment, err := h.service.GetDeployment(ctx, namespace, name)
	if err != nil {
		http.Error(w, "Deployment not found", http.StatusNotFound)
		return
	}

	updated, err := applyEnvChanges(deployment.Environment, req.Set, req.Unset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.logger.Info("Updating deployment environment",
		zap.String("namespace", namespace),
		zap.String("deployment", name),
		zap.Strings("set", sortedKeys(req.Set)),
		zap.Strings("unset", req.Unset),
	)

	if err := h.persistEnv(ctx, deployment, updated); err != nil {
		h.logger.Error("Failed to persist environment", zap.Error(err))
		http.Error(w, "Failed to save environment", http.StatusInternalServerError)
		return
	}
	deployment.Environment = updated

	// A static deployment has no process, so there is nothing to restart. The
	// variables are still recorded, because a redeploy as a dynamic type or a
	// build step may read them.
	restarted := false
	if deployment.Type != deployments.DeploymentTypeStatic && deployment.Type != deployments.DeploymentTypeNextJSStatic {
		workDir := filepath.Join(h.baseDeployPath, deployment.Namespace, deployment.Name)
		if err := h.processManager.Reconfigure(ctx, deployment, workDir); err != nil {
			h.logger.Error("Failed to reconfigure process", zap.Error(err))
			http.Error(w, fmt.Sprintf(
				"environment saved but the process could not be restarted: %v", err),
				http.StatusInternalServerError)
			return
		}
		restarted = true
	}

	writeJSON(w, map[string]any{
		"deployment_name": deployment.Name,
		"keys":            sortedKeys(updated),
		"restarted":       restarted,
		"updated_at":      time.Now(),
	})
}

// applyEnvChanges merges set and unset into the current environment.
//
// It returns a new map rather than mutating: a rejected change must leave the
// deployment's recorded environment untouched, and the caller still holds it.
func applyEnvChanges(current, set map[string]string, unset []string) (map[string]string, error) {
	updated := make(map[string]string, len(current)+len(set))
	for key, value := range current {
		updated[key] = value
	}

	for key, value := range set {
		if err := validateEnvKey(key); err != nil {
			return nil, err
		}
		// The value has to survive the trip to the process's environment. A
		// value systemd would discard is refused where it is set, not lost
		// where it is used.
		if err := deployments.ValidateEnvValue(key, value); err != nil {
			return nil, err
		}
		updated[key] = value
	}
	for _, key := range unset {
		if reservedEnvKeys[key] {
			return nil, fmt.Errorf("%s is set by the platform and cannot be removed", key)
		}
		delete(updated, key)
	}
	return updated, nil
}

// validateEnvKey rejects a name that is not a usable environment variable, or
// one the platform owns.
//
// The syntax rule lives with the environment-file writer, so the name a caller
// is allowed to set and the name that can actually be written are one rule
// rather than two that drift.
func validateEnvKey(key string) error {
	if reservedEnvKeys[key] {
		return fmt.Errorf("%s is set by the platform and cannot be overwritten", key)
	}
	return deployments.ValidateEnvName(key)
}

// persistEnv writes the environment back to the deployment row, sealed.
//
// This is also what migrates a deployment created before the column was
// encrypted: the row is read as plaintext and written back encrypted, so the
// plaintext form is not a permanent second format.
func (h *EnvHandler) persistEnv(ctx context.Context, deployment *deployments.Deployment, env map[string]string) error {
	encoded, err := h.service.EncodeEnvironment(env)
	if err != nil {
		return fmt.Errorf("encode environment: %w", err)
	}
	_, err = h.service.db.Exec(ctx,
		`UPDATE deployments SET environment = ?, updated_at = ? WHERE namespace = ? AND name = ?`,
		encoded, time.Now(), deployment.Namespace, deployment.Name)
	if err != nil {
		return fmt.Errorf("update deployments: %w", err)
	}
	return nil
}

// sortedKeys returns m's keys in a stable order, so output and logs do not
// reorder themselves between identical calls.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// writeJSON writes v as an indented JSON body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already written, so this can only be logged.
		return
	}
}

// parseFormEnv reads the env_<NAME> fields of a deploy upload.
//
// Deploy-time variables travel as separate form fields rather than one encoded
// blob so a value containing an '=' or a newline needs no escaping.
func parseFormEnv(values map[string][]string) (map[string]string, error) {
	env := make(map[string]string)
	for key, vals := range values {
		if !strings.HasPrefix(key, "env_") || len(vals) == 0 {
			continue
		}
		name := strings.TrimPrefix(key, "env_")
		if err := validateEnvKey(name); err != nil {
			return nil, err
		}
		if err := deployments.ValidateEnvValue(name, vals[0]); err != nil {
			return nil, err
		}
		env[name] = vals[0]
	}
	return env, nil
}

// withEntryPoint returns env plus the ENTRY_POINT a Node.js deployment needs.
//
// ENTRY_POINT is reserved, so parseFormEnv has already rejected any attempt to
// supply it; setting it here cannot silently override a user's value.
func withEntryPoint(env map[string]string, entryPoint string) map[string]string {
	out := make(map[string]string, len(env)+1)
	for key, value := range env {
		out[key] = value
	}
	out["ENTRY_POINT"] = entryPoint
	return out
}
