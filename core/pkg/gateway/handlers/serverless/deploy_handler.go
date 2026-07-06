package serverless

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// DeployFunction handles POST /v1/functions
// Deploys a new function or updates an existing one.
func (h *ServerlessHandlers) DeployFunction(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form (for WASM upload) or JSON
	contentType := r.Header.Get("Content-Type")

	var def serverless.FunctionDefinition
	var wasmBytes []byte

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Parse multipart form
		if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB max
			writeError(w, http.StatusBadRequest, "Failed to parse form: "+err.Error())
			return
		}

		// Get metadata from form field
		metadataStr := r.FormValue("metadata")
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &def); err != nil {
				writeError(w, http.StatusBadRequest, "Invalid metadata JSON: "+err.Error())
				return
			}
		}

		// Get name from form if not in metadata
		if def.Name == "" {
			def.Name = r.FormValue("name")
		}

		// Get namespace from form if not in metadata
		if def.Namespace == "" {
			def.Namespace = r.FormValue("namespace")
		}

		// Get other configuration fields from form
		if v := r.FormValue("is_public"); v != "" {
			def.IsPublic, _ = strconv.ParseBool(v)
		}
		if v := r.FormValue("is_internal"); v != "" {
			def.IsInternal, _ = strconv.ParseBool(v)
		}
		if v := r.FormValue("memory_limit_mb"); v != "" {
			def.MemoryLimitMB, _ = strconv.Atoi(v)
		}
		if v := r.FormValue("timeout_seconds"); v != "" {
			def.TimeoutSeconds, _ = strconv.Atoi(v)
		}
		if v := r.FormValue("retry_count"); v != "" {
			def.RetryCount, _ = strconv.Atoi(v)
		}
		if v := r.FormValue("retry_delay_seconds"); v != "" {
			def.RetryDelaySeconds, _ = strconv.Atoi(v)
		}

		// Get WASM file
		file, _, err := r.FormFile("wasm")
		if err != nil {
			writeError(w, http.StatusBadRequest, "WASM file required")
			return
		}
		defer file.Close()

		wasmBytes, err = io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Failed to read WASM file: "+err.Error())
			return
		}
	} else {
		// JSON body with base64-encoded WASM
		var req struct {
			serverless.FunctionDefinition
			WASMBase64 string `json:"wasm_base64"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}

		def = req.FunctionDefinition

		if req.WASMBase64 != "" {
			// Decode base64 WASM - for now, just reject this method
			writeError(w, http.StatusBadRequest, "Base64 WASM upload not supported, use multipart/form-data")
			return
		}
	}

	// Get namespace from JWT if not provided
	if def.Namespace == "" {
		def.Namespace = h.getNamespaceFromRequest(r)
	}

	if def.Name == "" {
		writeError(w, http.StatusBadRequest, "Function name required")
		return
	}
	if def.Namespace == "" {
		writeError(w, http.StatusBadRequest, "Namespace required")
		return
	}
	if len(wasmBytes) == 0 {
		writeError(w, http.StatusBadRequest, "WASM bytecode required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	oldFn, err := h.registry.Register(ctx, &def, wasmBytes)
	if err != nil {
		h.logger.Error("Failed to deploy function",
			zap.String("name", def.Name),
			zap.Error(err),
		)
		// Use the typed function-deploy code so clients can distinguish
		// "registry rejected this binary" from generic 500s.
		writeRPCError(w, http.StatusInternalServerError,
			httputil.ErrCodeFunctionDeploy,
			"Failed to deploy: "+err.Error())
		return
	}

	// Invalidate cache for the old version to ensure the new one is loaded
	if oldFn != nil {
		h.invoker.InvalidateCache(oldFn.WASMCID)
		h.logger.Debug("Invalidated function cache",
			zap.String("name", def.Name),
			zap.String("old_wasm_cid", oldFn.WASMCID),
		)
	}

	h.logger.Info("Function deployed",
		zap.String("name", def.Name),
		zap.String("namespace", def.Namespace),
	)

	// Fetch the deployed function to return
	fn, err := h.registry.Get(ctx, def.Namespace, def.Name, def.Version)
	if err != nil {
		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"message": "Function deployed successfully",
			"name":    def.Name,
		})
		return
	}

	// Register PubSub triggers from definition (deploy-time auto-registration)
	if h.triggerStore != nil && len(def.PubSubTopics) > 0 && fn != nil {
		_ = h.triggerStore.RemoveByFunction(ctx, fn.ID)
		for _, topic := range def.PubSubTopics {
			if _, err := h.triggerStore.Add(ctx, fn.ID, topic); err != nil {
				h.logger.Warn("Failed to register pubsub trigger",
					zap.String("topic", topic),
					zap.Error(err))
			} else if h.dispatcher != nil {
				h.dispatcher.InvalidateCache(ctx, def.Namespace, topic)
			}
		}
		// One Refresh after the batch — subscribes the dispatcher to libp2p
		// for every newly-added literal topic so WASM publishes from other
		// functions trigger this handler (bugboard #282). The periodic
		// refresh loop catches the rare add we miss here.
		if h.dispatcher != nil {
			if rerr := h.dispatcher.Refresh(ctx); rerr != nil {
				h.logger.Warn("PubSubDispatcher Refresh after deploy auto-register failed (periodic loop will retry)",
					zap.Error(rerr))
			}
		}
	}

	// Register Cron triggers from definition. Mirrors the PubSub branch above:
	// stale rows (from a previous deploy whose manifest had different cron
	// schedules) are cleared first, then the manifest's expressions are
	// re-added. Without this, manifest-driven cron schedules silently never
	// fired (feature #65 audit).
	//
	// We always run the RemoveByFunction even when CronExpressions is empty,
	// otherwise editing a manifest from `cron_expressions: ["0 3 * * *"]` to
	// `cron_expressions: []` would leave the old schedule in place.
	if h.cronStore != nil && fn != nil {
		if err := h.cronStore.RemoveByFunction(ctx, fn.ID); err != nil {
			h.logger.Warn("Failed to clear stale cron triggers",
				zap.String("function", def.Name),
				zap.Error(err))
		}
		// Dedupe identical expressions so a manifest accident
		// (`cron_expressions: ["0 3 * * *", "0 3 * * *"]`) doesn't fire the
		// function twice every tick.
		seen := make(map[string]struct{}, len(def.CronExpressions))
		for _, expr := range def.CronExpressions {
			if _, dup := seen[expr]; dup {
				continue
			}
			seen[expr] = struct{}{}
			if _, err := h.cronStore.Add(ctx, fn.ID, expr); err != nil {
				// Bad expression in a manifest is a user error worth surfacing
				// but not blocking the deploy — the function itself is fine,
				// only the schedule is dropped. Logged at WARN so operators
				// see it; the deploy response still reports success.
				h.logger.Warn("Failed to register cron trigger from manifest",
					zap.String("function", def.Name),
					zap.String("cron_expression", expr),
					zap.Error(err))
			}
		}
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"message":  "Function deployed successfully",
		"function": fn,
	})
}

// writeJSON writes JSON with status code
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError emits the canonical RPC error envelope (bug #212 fix).
//
// Derives the typed RPCErrorCode from the HTTP status — sufficient for
// most call sites. Callers that need to surface a specific code (e.g.
// FUNCTION_EXECUTION_FAILED on a 500 from the invoker) should use
// writeRPCError directly.
//
// Wire shape (always):
//
//	{"ok": false, "error": {"code": "...", "message": "...", "retryable": ...}}
func writeError(w http.ResponseWriter, status int, msg string) {
	httputil.WriteRPCError(w, status, codeForStatus(status), msg)
}

// writeRPCError is the typed helper for call sites that need to set a
// specific error code (e.g. distinguishing FUNCTION_EXECUTION_FAILED
// from a generic INTERNAL on a 500).
func writeRPCError(w http.ResponseWriter, status int, code httputil.RPCErrorCode, msg string, opts ...httputil.RPCErrorOption) {
	httputil.WriteRPCError(w, status, code, msg, opts...)
}

// codeForStatus maps HTTP status to the canonical RPCErrorCode. For
// statuses that map to multiple codes (500 → INTERNAL or
// FUNCTION_EXECUTION_FAILED), the caller picks via writeRPCError.
func codeForStatus(status int) httputil.RPCErrorCode {
	switch status {
	case http.StatusBadRequest:
		return httputil.ErrCodeValidationFailed
	case http.StatusUnauthorized:
		return httputil.ErrCodeUnauthorized
	case http.StatusForbidden:
		return httputil.ErrCodeForbidden
	case http.StatusNotFound:
		return httputil.ErrCodeNotFound
	case http.StatusConflict:
		return httputil.ErrCodeConflict
	case http.StatusRequestEntityTooLarge:
		return httputil.ErrCodePayloadTooLarge
	case http.StatusTooManyRequests:
		return httputil.ErrCodeRateLimited
	case http.StatusServiceUnavailable:
		return httputil.ErrCodeServiceUnavailable
	case http.StatusGatewayTimeout:
		return httputil.ErrCodeTimeout
	default:
		return httputil.ErrCodeInternal
	}
}
