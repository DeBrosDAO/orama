package vault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/shamir"
	"go.uber.org/zap"
)

// PushRequest is the client-facing request body.
type PushRequest struct {
	Identity string `json:"identity"` // 64 hex chars (SHA-256)
	Envelope string `json:"envelope"` // base64-encoded encrypted envelope
	Version  uint64 `json:"version"`  // Anti-rollback version counter
}

// PushResponse is returned to the client.
type PushResponse struct {
	Status    string `json:"status"`    // "ok" or "partial"
	AckCount  int    `json:"ack_count"`
	Total     int    `json:"total"`
	Quorum    int    `json:"quorum"`
	Threshold int    `json:"threshold"`
}

// guardianPushRequest is sent to each vault guardian.
type guardianPushRequest struct {
	Identity string `json:"identity"`
	Share    string `json:"share"`   // base64([x:1byte][y:rest])
	Version  uint64 `json:"version"`
}

// HandlePush processes POST /v1/vault/push.
func (h *Handlers) HandlePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxPushBodySize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req PushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if !isValidIdentity(req.Identity) {
		writeError(w, http.StatusBadRequest, "identity must be 64 hex characters")
		return
	}

	envelopeBytes, err := base64.StdEncoding.DecodeString(req.Envelope)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid base64 envelope")
		return
	}
	if len(envelopeBytes) == 0 {
		writeError(w, http.StatusBadRequest, "envelope must not be empty")
		return
	}

	if !h.rateLimiter.AllowPush(req.Identity) {
		w.Header().Set("Retry-After", "120")
		writeError(w, http.StatusTooManyRequests, "push rate limit exceeded for this identity")
		return
	}

	guardians, err := h.discoverGuardians(r.Context())
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "Vault push: guardian discovery failed", zap.Error(err))
		writeError(w, http.StatusServiceUnavailable, "no guardian nodes available")
		return
	}

	n := len(guardians)
	k := shamir.AdaptiveThreshold(n)
	quorum := shamir.WriteQuorum(n)

	shares, err := shamir.Split(envelopeBytes, n, k)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "Vault push: Shamir split failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to split envelope")
		return
	}

	// Fan out to guardians in parallel.
	ctx, cancel := context.WithTimeout(r.Context(), overallTimeout)
	defer cancel()

	var ackCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)

	for i, g := range guardians {
		go func(idx int, gd guardian) {
			defer wg.Done()

			share := shares[idx]
			// Serialize: [x:1byte][y:rest]
			shareBytes := make([]byte, 1+len(share.Y))
			shareBytes[0] = share.X
			copy(shareBytes[1:], share.Y)
			shareB64 := base64.StdEncoding.EncodeToString(shareBytes)

			guardianReq := guardianPushRequest{
				Identity: req.Identity,
				Share:    shareB64,
				Version:  req.Version,
			}
			reqBody, _ := json.Marshal(guardianReq)

			url := fmt.Sprintf("http://%s:%d/v1/vault/push", gd.IP, gd.Port)
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
			if err != nil {
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")

			resp, err := h.httpClient.Do(httpReq)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			io.Copy(io.Discard, resp.Body)

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				ackCount.Add(1)
			}
		}(i, g)
	}

	wg.Wait()

	// Wipe share data.
	for i := range shares {
		for j := range shares[i].Y {
			shares[i].Y[j] = 0
		}
	}

	ack := int(ackCount.Load())
	status := "ok"
	if ack < quorum {
		status = "partial"
	}

	writeJSON(w, http.StatusOK, PushResponse{
		Status:    status,
		AckCount:  ack,
		Total:     n,
		Quorum:    quorum,
		Threshold: k,
	})
}
