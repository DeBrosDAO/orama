package hostfunctions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// HTTPFetch makes an outbound HTTP request.
func (h *HostFunctions) HTTPFetch(ctx context.Context, method, url string, headers map[string]string, body []byte) ([]byte, error) {
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		h.logger.Error("http_fetch request creation error", zap.Error(err), zap.String("url", url))
		errorResp := map[string]interface{}{
			"error":  "failed to create request: " + err.Error(),
			"status": 0,
		}
		return json.Marshal(errorResp)
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.logger.Error("http_fetch transport error", zap.Error(err), zap.String("url", url))
		errorResp := map[string]interface{}{
			"error":  err.Error(),
			"status": 0, // Transport error
		}
		return json.Marshal(errorResp)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		h.logger.Error("http_fetch response read error", zap.Error(err), zap.String("url", url))
		errorResp := map[string]interface{}{
			"error":  "failed to read response: " + err.Error(),
			"status": resp.StatusCode,
		}
		return json.Marshal(errorResp)
	}

	// Encode response with status code
	response := map[string]interface{}{
		"status":  resp.StatusCode,
		"headers": resp.Header,
		"body":    string(respBody),
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, &serverless.HostFunctionError{Function: "http_fetch", Cause: fmt.Errorf("failed to marshal response: %w", err)}
	}

	return data, nil
}
