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

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/shamir"
	"go.uber.org/zap"
)

// PullRequest is the client-facing request body.
type PullRequest struct {
	Identity string `json:"identity"` // 64 hex chars
}

// PullResponse is returned to the client.
type PullResponse struct {
	Envelope  string `json:"envelope"`  // base64-encoded reconstructed envelope
	Collected int    `json:"collected"` // Number of shares collected
	Threshold int    `json:"threshold"` // K threshold used
}

// guardianPullRequest is sent to each vault guardian.
type guardianPullRequest struct {
	Identity string `json:"identity"`
}

// guardianPullResponse is the response from a guardian.
type guardianPullResponse struct {
	Share string `json:"share"` // base64([x:1byte][y:rest])
}

// HandlePull processes POST /v1/vault/pull.
func (h *Handlers) HandlePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxPullBodySize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req PullRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if !isValidIdentity(req.Identity) {
		writeError(w, http.StatusBadRequest, "identity must be 64 hex characters")
		return
	}

	if !h.rateLimiter.AllowPull(req.Identity) {
		w.Header().Set("Retry-After", "30")
		writeError(w, http.StatusTooManyRequests, "pull rate limit exceeded for this identity")
		return
	}

	guardians, err := h.discoverGuardians(r.Context())
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "Vault pull: guardian discovery failed", zap.Error(err))
		writeError(w, http.StatusServiceUnavailable, "no guardian nodes available")
		return
	}

	n := len(guardians)
	k := shamir.AdaptiveThreshold(n)

	// Fan out pull requests to all guardians.
	ctx, cancel := context.WithTimeout(r.Context(), overallTimeout)
	defer cancel()

	type shareResult struct {
		share shamir.Share
		ok    bool
	}

	results := make([]shareResult, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i, g := range guardians {
		go func(idx int, gd guardian) {
			defer wg.Done()

			guardianReq := guardianPullRequest{Identity: req.Identity}
			reqBody, _ := json.Marshal(guardianReq)

			url := fmt.Sprintf("http://%s:%d/v1/vault/pull", gd.IP, gd.Port)
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

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				io.Copy(io.Discard, resp.Body)
				return
			}

			var pullResp guardianPullResponse
			if err := json.NewDecoder(resp.Body).Decode(&pullResp); err != nil {
				return
			}

			shareBytes, err := base64.StdEncoding.DecodeString(pullResp.Share)
			if err != nil || len(shareBytes) < 2 {
				return
			}

			results[idx] = shareResult{
				share: shamir.Share{
					X: shareBytes[0],
					Y: shareBytes[1:],
				},
				ok: true,
			}
		}(i, g)
	}

	wg.Wait()

	// Collect successful shares.
	shares := make([]shamir.Share, 0, n)
	for _, r := range results {
		if r.ok {
			shares = append(shares, r.share)
		}
	}

	if len(shares) < k {
		h.logger.ComponentError(logging.ComponentGeneral, "Vault pull: not enough shares",
			zap.Int("collected", len(shares)), zap.Int("total", n), zap.Int("threshold", k))
		writeError(w, http.StatusServiceUnavailable,
			fmt.Sprintf("not enough shares: collected %d of %d required (contacted %d guardians)", len(shares), k, n))
		return
	}

	// Shamir combine to reconstruct envelope.
	envelope, err := shamir.Combine(shares[:k])
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "Vault pull: Shamir combine failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to reconstruct envelope")
		return
	}

	// Wipe collected shares.
	for i := range shares {
		for j := range shares[i].Y {
			shares[i].Y[j] = 0
		}
	}

	envelopeB64 := base64.StdEncoding.EncodeToString(envelope)

	// Wipe envelope.
	for i := range envelope {
		envelope[i] = 0
	}

	writeJSON(w, http.StatusOK, PullResponse{
		Envelope:  envelopeB64,
		Collected: len(shares),
		Threshold: k,
	})
}
