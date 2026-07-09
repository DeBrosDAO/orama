package vault

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/shamir"
	"go.uber.org/zap"
)

// PushRequest is the client-facing request body.
type PushRequest struct {
	Identity  string `json:"identity"`  // 64 hex chars (= SHA-256(pubkey))
	Envelope  string `json:"envelope"`  // base64-encoded encrypted envelope
	Version   uint64 `json:"version"`   // Anti-rollback version counter
	PubKey    string `json:"pubkey"`    // hex Ed25519 public key (32 bytes)
	Signature string `json:"signature"` // hex Ed25519 sig over the push message
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
	Identity  string `json:"identity"`
	Share     string `json:"share"`     // base64([x:1byte][y:rest])
	Version   uint64 `json:"version"`
	Threshold int    `json:"threshold"` // K the envelope was split with (persisted for reads)
	PubKey    string `json:"pubkey"`    // forwarded for guardian-side ownership check
	Signature string `json:"signature"` // forwarded for guardian-side ownership check
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

	// Per-IP limit is checked BEFORE the ownership proof (see HandlePull for the
	// rationale). The generic "rate limited" message does not reveal whether the
	// identity exists.
	if !h.ipRateLimiter.AllowPush(clientIP(r)) {
		w.Header().Set("Retry-After", strconv.Itoa(ipRetryAfterSeconds))
		writeError(w, http.StatusTooManyRequests, "rate limited")
		return
	}

	// Ownership proof: identity = SHA-256(pubkey) + a valid Ed25519 signature
	// over the push message. Only the identity's key holder may write it — this
	// is verified again at each guardian, so the gateway is not a trusted point.
	if !verifyPush(req.Identity, req.Version, req.PubKey, req.Signature) {
		writeError(w, http.StatusUnauthorized, "invalid ownership signature")
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
	var conflictCount atomic.Int32
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
				Identity:  req.Identity,
				Share:     shareB64,
				Version:   req.Version,
				Threshold: k,
				PubKey:    req.PubKey,
				Signature: req.Signature,
			}
			reqBody, _ := json.Marshal(guardianReq)

			url := fmt.Sprintf("http://%s:%d/v1/vault/push", gd.IP, gd.Port)
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
			if err != nil {
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")

			// Authenticate to this guardian first; without a valid session token
			// the guardian rejects the push with 401 and no share is stored.
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
			io.Copy(io.Discard, resp.Body)

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				ackCount.Add(1)
			} else if resp.StatusCode == http.StatusConflict {
				conflictCount.Add(1)
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
	if ack >= quorum {
		writeJSON(w, http.StatusOK, PushResponse{
			Status:    "ok",
			AckCount:  ack,
			Total:     n,
			Quorum:    quorum,
			Threshold: k,
		})
		return
	}

	// If the write was rejected because the version already exists on a quorum
	// of guardians, surface a distinct 409 so the client re-reads and bumps the
	// version instead of retrying blindly against a perceived outage.
	if int(conflictCount.Load()) >= quorum {
		writeJSON(w, http.StatusConflict, PushResponse{
			Status:    "version_conflict",
			AckCount:  ack,
			Total:     n,
			Quorum:    quorum,
			Threshold: k,
		})
		return
	}

	// Fewer than the write quorum of guardians stored the share. Because the
	// write quorum exceeds the read threshold, a sub-quorum write may be
	// unrecoverable, so report failure (HTTP 503) rather than a phantom success —
	// the previous code returned HTTP 200 here even with zero ACKs, hiding total
	// write failure from the client.
	writeJSON(w, http.StatusServiceUnavailable, PushResponse{
		Status:    "insufficient_quorum",
		AckCount:  ack,
		Total:     n,
		Quorum:    quorum,
		Threshold: k,
	})
}
