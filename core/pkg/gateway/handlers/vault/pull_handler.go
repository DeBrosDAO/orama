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
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/shamir"
	"go.uber.org/zap"
)

// PullRequest is the client-facing request body.
type PullRequest struct {
	Identity  string `json:"identity"`  // 64 hex chars (= SHA-256(pubkey))
	PubKey    string `json:"pubkey"`    // hex Ed25519 public key (32 bytes)
	Signature string `json:"signature"` // hex Ed25519 sig over the pull message
	Timestamp int64  `json:"timestamp"` // unix seconds, bound into the signature
}

// PullResponse is returned to the client.
type PullResponse struct {
	Envelope  string `json:"envelope"`  // base64-encoded reconstructed envelope
	Collected int    `json:"collected"` // Number of shares collected
	Threshold int    `json:"threshold"` // K threshold used
}

// guardianPullRequest is sent to each vault guardian.
type guardianPullRequest struct {
	Identity  string `json:"identity"`
	PubKey    string `json:"pubkey"`    // forwarded for guardian-side ownership check
	Signature string `json:"signature"` // forwarded for guardian-side ownership check
	Timestamp int64  `json:"timestamp"` // forwarded for guardian-side ownership check
}

// guardianPullResponse is the response from a guardian.
type guardianPullResponse struct {
	Share     string `json:"share"`     // base64([x:1byte][y:rest])
	Version   uint64 `json:"version"`   // version this share belongs to
	Threshold int    `json:"threshold"` // K the envelope was split with
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

	// Ownership proof: identity = SHA-256(pubkey) + a fresh, valid Ed25519
	// signature over the pull message. Without this, anyone who knows an identity
	// could read its (encrypted) blob — the password-oracle. Re-verified at each
	// guardian too.
	if !verifyPull(req.Identity, req.Timestamp, time.Now().Unix(), req.PubKey, req.Signature) {
		writeError(w, http.StatusUnauthorized, "invalid ownership signature")
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
		share     shamir.Share
		version   uint64
		threshold int
		ok        bool
	}

	results := make([]shareResult, n)
	var wg sync.WaitGroup
	wg.Add(n)

	for i, g := range guardians {
		go func(idx int, gd guardian) {
			defer wg.Done()

			guardianReq := guardianPullRequest{
				Identity:  req.Identity,
				PubKey:    req.PubKey,
				Signature: req.Signature,
				Timestamp: req.Timestamp,
			}
			reqBody, _ := json.Marshal(guardianReq)

			url := fmt.Sprintf("http://%s:%d/v1/vault/pull", gd.IP, gd.Port)
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
			if err != nil {
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")

			// Authenticate to this guardian first; without a valid session token
			// the guardian rejects the pull with 401 and returns no share.
			token, err := h.authenticateGuardian(ctx, gd.IP, gd.Port, req.Identity)
			if err != nil {
				return
			}
			httpReq.Header.Set("X-Session-Token", token)

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
				version:   pullResp.Version,
				threshold: pullResp.Threshold,
				ok:        true,
			}
		}(i, g)
	}

	wg.Wait()

	// Group collected shares by version. Combining shares from different
	// versions silently yields garbage (Shamir has no cross-version check), so
	// reconstruct only from a single version — the newest one that has at least
	// its stored threshold of shares. The threshold is the one the envelope was
	// split with (persisted per share), NOT one recomputed from the current
	// cluster size, so fleet changes don't brick existing backups.
	byVersion := make(map[uint64][]shamir.Share)
	threshByVersion := make(map[uint64]int)
	collected := 0
	for _, r := range results {
		if !r.ok {
			continue
		}
		collected++
		byVersion[r.version] = append(byVersion[r.version], r.share)
		if r.threshold > threshByVersion[r.version] {
			threshByVersion[r.version] = r.threshold
		}
	}

	var (
		bestShares []shamir.Share
		bestK      int
		bestVer    uint64
		found      bool
	)
	for ver, vShares := range byVersion {
		vk := threshByVersion[ver]
		if vk <= 0 {
			vk = k // legacy shares without a stored threshold
		}
		if len(vShares) >= vk && (!found || ver > bestVer) {
			bestShares, bestK, bestVer, found = vShares, vk, ver, true
		}
	}

	if !found {
		h.logger.ComponentError(logging.ComponentGeneral, "Vault pull: no version-consistent read set",
			zap.Int("collected", collected), zap.Int("total", n), zap.Int("threshold", k))
		writeError(w, http.StatusServiceUnavailable,
			fmt.Sprintf("not enough consistent shares: collected %d (contacted %d guardians)", collected, n))
		return
	}

	// Shamir combine exactly bestK shares of the chosen version.
	envelope, err := shamir.Combine(bestShares[:bestK])
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "Vault pull: Shamir combine failed", zap.Error(err))
		writeError(w, http.StatusInternalServerError, "failed to reconstruct envelope")
		return
	}

	// Wipe all collected share material.
	for i := range results {
		if !results[i].ok {
			continue
		}
		for j := range results[i].share.Y {
			results[i].share.Y[j] = 0
		}
	}

	envelopeB64 := base64.StdEncoding.EncodeToString(envelope)
	for i := range envelope {
		envelope[i] = 0
	}

	writeJSON(w, http.StatusOK, PullResponse{
		Envelope:  envelopeB64,
		Collected: len(bestShares),
		Threshold: bestK,
	})
}
