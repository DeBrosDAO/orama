package serverless

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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
		writeError(w, http.StatusInternalServerError, "Failed to deploy: "+err.Error())
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

// writeError writes a standardized JSON error
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}
