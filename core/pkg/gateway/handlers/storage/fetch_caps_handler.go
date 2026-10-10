package storage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/capability"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/ipfs"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/serverless"
	"go.uber.org/zap"
)

// Fetch capabilities (bugboard #266): the owner mints them, a client downloads
// with one and no identity (relayed_download.go).

const (
	// FetchCapsPath is where fetch capabilities are minted; a revoke names the
	// id after it.
	FetchCapsPath = "/v1/storage/fetch-caps"

	// RevokeKeyHeader carries the revoke key that proves a revoke by id: the
	// revoke_key the mint returned beside that capability.
	RevokeKeyHeader = "X-Orama-Revoke-Key"

	// maxFetchCapMintBody bounds a mint request. A real one is under 200 bytes.
	maxFetchCapMintBody = 4 << 10

	// fetchCapMinTTLSeconds and fetchCapMaxTTLSeconds are the lifetimes a mint
	// may ask for (1 hour to 7 days), the issuer's own bounds in seconds.
	fetchCapMinTTLSeconds = int64(capability.FetchMinTTL / time.Second)
	fetchCapMaxTTLSeconds = int64(capability.MaxTTL / time.Second)
)

// FetchCapGate mints, checks and revokes fetch capabilities. *capability.Issuer
// is the one the gateway wires; without one every fetch-capability route
// answers 503 and says why.
type FetchCapGate interface {
	MintFetchCaps(ctx context.Context, namespace, cid, issuerDevice string, count int, ttl time.Duration) ([]serverless.FetchCap, error)
	RevokeFetchCap(ctx context.Context, namespace, id, revokeKey string) error
	CheckFetch(token, namespace, cid string) (*capability.FetchClaims, error)
}

// SetFetchCaps wires the fetch-capability gate. Called once at gateway start.
func (h *Handlers) SetFetchCaps(gate FetchCapGate) { h.fetchCaps = gate }

type mintFetchCapsRequest struct {
	CID        string `json:"cid"`
	Count      int    `json:"count"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type mintFetchCapsResponse struct {
	Namespace string                `json:"namespace"`
	CID       string                `json:"cid"`
	Caps      []serverless.FetchCap `json:"caps"`
}

// FetchCapsHandler serves POST /v1/storage/fetch-caps (mint) and
// DELETE /v1/storage/fetch-caps/{id} (revoke, with the id's X-Orama-Revoke-Key).
func (h *Handlers) FetchCapsHandler(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, FetchCapsPath), "/")
	switch {
	case id == "" && r.Method == http.MethodPost:
		h.mintFetchCaps(w, r)
	case id != "" && r.Method == http.MethodDelete:
		h.revokeFetchCap(w, r, id)
	default:
		httputil.WriteRPCError(w, http.StatusMethodNotAllowed, httputil.ErrCodeValidationFailed,
			"mint with POST /v1/storage/fetch-caps, revoke with DELETE /v1/storage/fetch-caps/{id}")
	}
}

func (h *Handlers) mintFetchCaps(w http.ResponseWriter, r *http.Request) {
	namespace, ok := h.fetchCapCaller(w, r)
	if !ok {
		return
	}
	req, ok := decodeMintFetchCaps(w, r)
	if !ok {
		return
	}
	device := callerDevice(r)
	if device == "" {
		writeFetchCapError(w, http.StatusForbidden, CodeFetchCapDeviceRequired,
			"a fetch capability is issued by a device: this session is bound to none")
		return
	}
	if !h.authorizeDownload(w, r, req.CID, namespace) {
		return
	}
	caps, err := h.fetchCaps.MintFetchCaps(r.Context(), namespace, req.CID, device, req.Count,
		time.Duration(req.TTLSeconds)*time.Second)
	if err != nil {
		h.logger.ComponentError(logging.ComponentGeneral, "failed to mint fetch capabilities",
			zap.Error(err), zap.String("namespace", namespace))
		httputil.WriteRPCError(w, http.StatusInternalServerError, httputil.ErrCodeInternal,
			"failed to mint the fetch capabilities")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, mintFetchCapsResponse{Namespace: namespace, CID: req.CID, Caps: caps})
}

func (h *Handlers) revokeFetchCap(w http.ResponseWriter, r *http.Request, id string) {
	namespace, ok := h.fetchCapCaller(w, r)
	if !ok {
		return
	}
	if !capability.ValidFetchCapID(id) {
		httputil.WriteRPCError(w, http.StatusBadRequest, httputil.ErrCodeValidationFailed,
			"the id is not a fetch capability id (32 hex characters)")
		return
	}
	revokeKey := strings.TrimSpace(r.Header.Get(RevokeKeyHeader))
	if revokeKey == "" {
		writeFetchCapError(w, http.StatusForbidden, CodeFetchCapRevokeKeyInvalid,
			"revoking a fetch capability takes its revoke key in the "+RevokeKeyHeader+" header")
		return
	}
	err := h.fetchCaps.RevokeFetchCap(r.Context(), namespace, id, revokeKey)
	switch {
	case err == nil:
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"revoked": id})
	case errors.Is(err, capability.ErrFetchRevokeKeyInvalid):
		writeFetchCapError(w, http.StatusForbidden, CodeFetchCapRevokeKeyInvalid,
			"the revoke key is not the one issued for this fetch capability")
	default:
		h.logger.ComponentError(logging.ComponentGeneral, "failed to revoke a fetch capability",
			zap.Error(err), zap.String("namespace", namespace))
		writeFetchCapError(w, http.StatusServiceUnavailable, CodeFetchCapUnavailable,
			"the fetch capability could not be revoked right now")
	}
}

// fetchCapCaller returns the namespace of a credentialed fetch-capability call,
// or writes the refusal.
func (h *Handlers) fetchCapCaller(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.fetchCaps == nil {
		writeFetchCapError(w, http.StatusServiceUnavailable, CodeFetchCapUnavailable,
			"this gateway cannot mint or check fetch capabilities: it has no cluster secret")
		return "", false
	}
	namespace := h.getNamespaceFromContext(r.Context())
	if namespace == "" {
		httputil.WriteRPCError(w, http.StatusUnauthorized, httputil.ErrCodeUnauthorized, "namespace required")
		return "", false
	}
	return namespace, true
}

func decodeMintFetchCaps(w http.ResponseWriter, r *http.Request) (mintFetchCapsRequest, bool) {
	var req mintFetchCapsRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFetchCapMintBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		httputil.WriteRPCError(w, http.StatusBadRequest, httputil.ErrCodeValidationFailed,
			`the body is {"cid","count","ttl_seconds"}`)
		return req, false
	}
	if err := ipfs.CanonicalCID(req.CID); err != nil {
		httputil.WriteRPCError(w, http.StatusBadRequest, httputil.ErrCodeValidationFailed, err.Error())
		return req, false
	}
	if req.Count < 1 || req.Count > capability.MaxFetchCapsPerMint {
		httputil.WriteRPCError(w, http.StatusBadRequest, httputil.ErrCodeValidationFailed,
			"count is between 1 and 64")
		return req, false
	}
	if req.TTLSeconds < fetchCapMinTTLSeconds || req.TTLSeconds > fetchCapMaxTTLSeconds {
		httputil.WriteRPCError(w, http.StatusBadRequest, httputil.ErrCodeValidationFailed,
			"ttl_seconds is between 3600 (1 hour) and 604800 (7 days)")
		return req, false
	}
	return req, true
}

// callerDevice is the device the caller's session is bound to, or "".
func callerDevice(r *http.Request) string {
	claims, _ := r.Context().Value(ctxkeys.JWT).(*gwauth.JWTClaims)
	if claims == nil {
		return ""
	}
	return strings.TrimSpace(claims.Did)
}
